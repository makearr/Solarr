// Package cachebudget is ADR-60's own bounded cache: current size measured
// (not estimated) per namespace, age-ordered eviction within whichever
// namespace is over budget, and a manual clear that is always safe
// (Invariant 5 -- nothing in the cache is the only copy of anything).
//
// The two namespaces are architecturally different and are handled
// differently: images is a flat directory of files (internal/media/music's own
// artwork cache, evicted by removing files through fsx); metadata is
// internal/metadata/metacache's own SQLite-backed cache (evicted by deleting rows).
// Deleting metadata rows frees logical bytes immediately but does not
// shrink metadata.db's own physical file without a VACUUM this package
// deliberately does not run -- a named simplification, not an oversight
// (see internal/metadata/metacache's own SizeBytes doc comment).
package cachebudget

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"path/filepath"
	"sort"
	"time"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/metadata/metacache"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// NamespaceReport is one namespace's measured size.
type NamespaceReport struct {
	Bytes int64
}

// percentOf avoids overflowing the intermediate multiplication for large byte budgets.
func percentOf(bytes, percent int64) int64 {
	if bytes <= 0 || percent <= 0 {
		return 0
	}
	if percent >= 100 {
		return bytes
	}
	return (bytes/100)*percent + (bytes%100)*percent/100
}

// Report is what one measurement pass found, both namespaces together.
type Report struct {
	Images, Metadata NamespaceReport
	MeasuredAt       time.Time
	// DiskTotalBytes and DiskFreeBytes are the filesystem under the cache, 0 when unreadable.
	DiskTotalBytes, DiskFreeBytes int64
}

// Result is what one enforcement pass did, on top of its own final Report.
type Result struct {
	Report                         Report
	ImagesEvicted, MetadataEvicted int64
	// ShrunkToBytes is the ceiling the disk's free space held the cache to, 0 when MaxBytes held.
	ShrunkToBytes int64
}

// Config is everything a Sweep needs. Every field is required.
type Config struct {
	FS fsx.FS
	// Dir is the cache directory, whose filesystem's free space MinFreePercent keeps.
	Dir       string
	ImagesDir string
	Metadata  metacache.Cache
	Cache     *settings.Cache
	Clock     clock.Clock
	Logger    *slog.Logger
}

// Sweep measures, enforces and clears the cache.
type Sweep struct {
	fs        fsx.FS
	dir       string
	imagesDir string
	metadata  metacache.Cache
	cache     *settings.Cache
	clock     clock.Clock
	logger    *slog.Logger
}

// NewSweep builds a Sweep.
func NewSweep(config Config) *Sweep {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Sweep{
		fs: config.FS, dir: config.Dir, imagesDir: config.ImagesDir, metadata: config.Metadata,
		cache: config.Cache, clock: config.Clock, logger: logger,
	}
}

// Measure answers each namespace's current size, without evicting anything,
// and does not persist the result -- callers that want it recorded call
// Enforce, which always measures at the end of its own pass.
func (s *Sweep) Measure(ctx context.Context) (Report, error) {
	_, imagesBytes, err := s.listImages()
	if err != nil {
		return Report{}, fmt.Errorf("cachebudget: measure images: %w", err)
	}

	metadataBytes, err := s.metadata.SizeBytes(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("cachebudget: measure metadata: %w", err)
	}

	report := Report{
		Images:     NamespaceReport{Bytes: imagesBytes},
		Metadata:   NamespaceReport{Bytes: metadataBytes},
		MeasuredAt: s.clock.Now(),
	}
	if total, free, err := s.fs.DiskUsage(s.dir); err == nil {
		report.DiskTotalBytes, report.DiskFreeBytes = total, free
	}
	return report, nil
}

// heldToFreeSpace lowers the budget's ceiling by however far the disk under the cache is below MinFreePercent free;
// answers the lowered ceiling, or 0 when the budget stands (user, 2026-10-23).
func (s *Sweep) heldToFreeSpace(ctx context.Context, budget *settings.CacheBudget) (int64, error) {
	if budget.MinFreePercent <= 0 {
		return 0, nil
	}
	now, err := s.Measure(ctx)
	if err != nil {
		return 0, err
	}
	if now.DiskTotalBytes <= 0 {
		return 0, nil
	}
	short := percentOf(now.DiskTotalBytes, budget.MinFreePercent) - now.DiskFreeBytes
	if short <= 0 {
		return 0, nil
	}
	used := now.Images.Bytes
	if now.Metadata.Bytes > 0 && used > int64(^uint64(0)>>1)-now.Metadata.Bytes {
		used = math.MaxInt64
	} else {
		used += now.Metadata.Bytes
	}
	ceiling := max(0, used-short)
	if ceiling >= budget.MaxBytes {
		return 0, nil
	}
	s.logger.WarnContext(ctx, "cache disk short of its free space: shrinking the cache",
		slog.Int64("short_bytes", short), slog.Int64("ceiling_bytes", ceiling))
	budget.MaxBytes = ceiling
	return ceiling, nil
}

// Enforce applies ADR-60's two eviction triggers in order -- max_age
// unconditionally, then budget-triggered age-ordered eviction within
// whichever namespace is still over its own share -- then records the
// final measurement.
func (s *Sweep) Enforce(ctx context.Context, budget settings.CacheBudget) (Result, error) {
	cutoff := s.clock.Now().AddDate(0, 0, -int(budget.MaxAgeDays))

	imagesAgeEvicted, err := s.pruneImagesOlderThan(cutoff)
	if err != nil {
		return Result{}, fmt.Errorf("cachebudget: prune images by age: %w", err)
	}
	metadataAgeEvicted, err := s.metadata.DeleteOlderThan(ctx, cutoff)
	if err != nil {
		return Result{}, fmt.Errorf("cachebudget: prune metadata by age: %w", err)
	}

	shrunkTo, err := s.heldToFreeSpace(ctx, &budget)
	if err != nil {
		return Result{}, fmt.Errorf("cachebudget: free space: %w", err)
	}
	imagesBudgetBytes := percentOf(budget.MaxBytes, budget.ImagesSharePercent)
	metadataBudgetBytes := percentOf(budget.MaxBytes, budget.MetadataSharePercent)

	imagesBudgetEvicted, err := s.evictOldestImagesUntilUnder(imagesBudgetBytes)
	if err != nil {
		return Result{}, fmt.Errorf("cachebudget: evict images by budget: %w", err)
	}
	metadataBudgetEvicted, err := s.evictOldestMetadataUntilUnder(ctx, metadataBudgetBytes)
	if err != nil {
		return Result{}, fmt.Errorf("cachebudget: evict metadata by budget: %w", err)
	}

	report, err := s.Measure(ctx)
	if err != nil {
		return Result{}, err
	}
	if err := s.cache.SetCacheMeasurement(ctx, s.clock, settings.CacheMeasurement{
		ImagesBytes: report.Images.Bytes, MetadataBytes: report.Metadata.Bytes,
		MeasuredAt: report.MeasuredAt.Unix(), DiskTotalBytes: report.DiskTotalBytes, DiskFreeBytes: report.DiskFreeBytes,
		ShrunkToBytes: shrunkTo,
	}); err != nil {
		return Result{}, fmt.Errorf("cachebudget: record measurement: %w", err)
	}

	return Result{
		Report:          report,
		ImagesEvicted:   imagesAgeEvicted + imagesBudgetEvicted,
		MetadataEvicted: metadataAgeEvicted + metadataBudgetEvicted,
		ShrunkToBytes:   shrunkTo,
	}, nil
}

// Clear removes everything in both namespaces (Invariant 5: always safe --
// a miss just costs a re-fetch, never data).
func (s *Sweep) Clear(ctx context.Context) error {
	files, _, err := s.listImages()
	if err != nil {
		return fmt.Errorf("cachebudget: clear: list images: %w", err)
	}
	var errs []error
	for _, file := range files {
		if err := s.fs.Remove(file.path); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("cachebudget: clear: %w", errors.Join(errs...))
	}

	if err := s.metadata.Clear(ctx); err != nil {
		return fmt.Errorf("cachebudget: clear: %w", err)
	}

	now := s.clock.Now()
	return s.cache.SetCacheMeasurement(ctx, s.clock, settings.CacheMeasurement{MeasuredAt: now.Unix()})
}

type imageFile struct {
	path    string
	size    int64
	modTime time.Time
}

// listImages answers every file directly under imagesDir and their total
// size. A missing directory (a fresh install, the artwork job has never run)
// is not an error -- it means the namespace is empty.
func (s *Sweep) listImages() ([]imageFile, int64, error) {
	entries, err := s.fs.ReadDir(s.imagesDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, 0, nil
		}
		return nil, 0, err
	}

	var files []imageFile
	var total int64
	for _, entry := range entries {
		if entry.IsDir {
			continue
		}
		path := filepath.Join(s.imagesDir, entry.Name)
		info, err := s.fs.Stat(path)
		if err != nil {
			// Removed between ReadDir and Stat -- not this sweep's problem.
			s.logger.Debug("cache sweep: file vanished mid-walk", slog.String("path", path), slog.Any("error", err))
			continue
		}
		files = append(files, imageFile{path: path, size: info.Size, modTime: info.ModTime})
		total += info.Size
	}
	return files, total, nil
}

func (s *Sweep) pruneImagesOlderThan(cutoff time.Time) (int64, error) {
	files, _, err := s.listImages()
	if err != nil {
		return 0, err
	}
	var evicted int64
	for _, file := range files {
		if !file.modTime.Before(cutoff) {
			continue
		}
		if err := s.fs.Remove(file.path); err != nil {
			s.logger.Warn("cache sweep: could not remove an aged-out image",
				slog.String("path", file.path), slog.Any("error", err))
			continue
		}
		evicted++
	}
	return evicted, nil
}

func (s *Sweep) evictOldestImagesUntilUnder(budgetBytes int64) (int64, error) {
	files, total, err := s.listImages()
	if err != nil {
		return 0, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].modTime.Before(files[j].modTime) })

	var evicted int64
	for _, file := range files {
		if total <= budgetBytes {
			break
		}
		if err := s.fs.Remove(file.path); err != nil {
			s.logger.Warn("cache sweep: could not remove an over-budget image",
				slog.String("path", file.path), slog.Any("error", err))
			continue
		}
		total -= file.size
		evicted++
	}
	return evicted, nil
}

// evictOldestMetadataUntilUnder removes one entry at a time, re-measuring
// after each -- deliberately not a larger batch. library.Recycle bin's own
// Sweep once over-evicted by purging a whole batch before re-checking the
// running total against a small budget; the fix there was the same one
// applied here from the start: evict one entry, then check again.
func (s *Sweep) evictOldestMetadataUntilUnder(ctx context.Context, budgetBytes int64) (int64, error) {
	var evicted int64
	for {
		size, err := s.metadata.SizeBytes(ctx)
		if err != nil {
			return evicted, err
		}
		if size <= budgetBytes {
			return evicted, nil
		}
		removed, err := s.metadata.DeleteOldest(ctx, 1)
		if err != nil {
			return evicted, err
		}
		evicted += removed
		if removed == 0 {
			// Over budget with nothing left to evict: the budget itself is
			// smaller than what a single measurement rounds down to, or
			// every remaining entry is already gone. Either way, looping
			// further would spin forever.
			return evicted, nil
		}
	}
}
