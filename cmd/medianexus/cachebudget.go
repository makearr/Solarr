package main

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/makearr/MediaNexus/internal/apiserver"
	"github.com/makearr/MediaNexus/internal/cachebudget"
	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/jobs"
	"github.com/makearr/MediaNexus/internal/metacache"
	"github.com/makearr/MediaNexus/internal/settings"
)

// cacheJobType is the registered type of ADR-60's cache maintenance sweep.
const cacheJobType = jobs.Type("settings.cache_maintenance")

// cacheActionKey deduplicates the sweep: global, not per-namespace, the
// same shape capacity's own sweep uses for its own global pass.
const cacheActionKey = "settings.cache_maintenance"

var cacheJobDefinition = jobs.Definition{
	Type: cacheJobType, Pool: jobs.PoolMaintenance, Mutating: true, MaxAttempts: 3,
}

// cacheJob applies ADR-60's budget (max_age unconditionally, then budget-
// triggered age-ordered eviction per namespace) and records what it found.
type cacheJob struct {
	sweep  *cachebudget.Sweep
	cache  *settings.Cache
	logger *slog.Logger
}

func (j cacheJob) Run(ctx context.Context, _ jobs.Job) error {
	budget := j.cache.Snapshot().CacheBudget()
	result, err := j.sweep.Enforce(ctx, budget)
	if err != nil {
		return fmt.Errorf("cache maintenance: %w", err)
	}
	j.logger.Info("cache maintenance finished",
		slog.Int64("images_bytes", result.Report.Images.Bytes),
		slog.Int64("metadata_bytes", result.Report.Metadata.Bytes),
		slog.Int64("images_evicted", result.ImagesEvicted),
		slog.Int64("metadata_evicted", result.MetadataEvicted))
	return nil
}

// registerCacheJob wires ADR-60's cache maintenance sweep into the registry
// and the executor, and keeps the built Sweep on p for the API adapter
// (cacheManagerAdapter, wired in startServer) to reuse.
func (p *process) registerCacheJob(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(cacheJobDefinition); err != nil {
		return err
	}
	p.cacheSweep = cachebudget.NewSweep(cachebudget.Config{
		FS: p.fsys, ImagesDir: filepath.Join(p.config.Paths.CacheDir, "images"),
		Metadata: metacache.New(p.metadata), Cache: p.cache, Clock: clock.NewSystem(), Logger: p.logger,
	})
	executor.Handle(cacheJobType, cacheJob{sweep: p.cacheSweep, cache: p.cache, logger: p.logger})
	return nil
}

// cacheManagerAdapter satisfies apiserver.CacheManager over
// internal/cachebudget.Sweep and internal/settings.Cache, the same
// interfaces-in-apiserver/adapters-in-cmd/medianexus shape every other
// library-backed endpoint already uses.
type cacheManagerAdapter struct {
	sweep *cachebudget.Sweep
	cache *settings.Cache
	clock clock.Clock
}

func (a cacheManagerAdapter) Report(_ context.Context) (apiserver.CacheReport, error) {
	budget := a.cache.Snapshot().CacheBudget()
	report := apiserver.CacheReport{
		Budget: apiserver.CacheBudget{
			MaxBytes: budget.MaxBytes, MaxAgeDays: budget.MaxAgeDays,
			ImagesSharePercent: budget.ImagesSharePercent, MetadataSharePercent: budget.MetadataSharePercent,
		},
	}
	if measurement, found := a.cache.Snapshot().CacheMeasurement(); found {
		report.ImagesBytes = &measurement.ImagesBytes
		report.MetadataBytes = &measurement.MetadataBytes
		measuredAt := time.Unix(measurement.MeasuredAt, 0).UTC()
		report.MeasuredAt = &measuredAt
	}
	return report, nil
}

func (a cacheManagerAdapter) SetBudget(ctx context.Context, budget apiserver.CacheBudget) error {
	return a.cache.SetCacheBudget(ctx, a.clock, settings.CacheBudget{
		MaxBytes: budget.MaxBytes, MaxAgeDays: budget.MaxAgeDays,
		ImagesSharePercent: budget.ImagesSharePercent, MetadataSharePercent: budget.MetadataSharePercent,
	})
}

func (a cacheManagerAdapter) Clear(ctx context.Context) error {
	return a.sweep.Clear(ctx)
}

// enqueueCacheMaintenance submits one cache sweep. Registered as a task
// with internal/jobs.TaskRunner (todo 4.19), so it fires on its own
// schedule.
func (p *process) enqueueCacheMaintenance(ctx context.Context) (jobs.EnqueueResult, error) {
	result, err := p.queue.Enqueue(ctx, jobs.Request{
		Type: cacheJobType, ActionKey: cacheActionKey, Priority: jobs.PriorityLow, Payload: []byte("{}"),
	})
	if err != nil {
		p.logger.Warn("enqueue cache maintenance", slog.Any("error", err))
	}
	return result, err
}
