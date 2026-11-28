package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// CategoryScope names one category and the root folders it spans -- settings.db
// policy (ADR-25), so the caller supplies it, the same shape as ReconcileScope.
type CategoryScope struct {
	CategoryID    int64
	RootFolderIDs []int64
	// MediaType is the library's own: only works of it count, whatever lies under its folders. Empty counts every type.
	MediaType string
	// OfflineRootFolderIDs is the subset of RootFolderIDs settings.db declares offline (section 10.4), composed in Go (ADR-25); "files" stays the true total and "files_offline" counts the unreachable part (todo 4.18).
	OfflineRootFolderIDs []int64
	// WantedVideoSubtypes are the video subtypes the library's wanted count includes; nil when it did not switch videos on.
	WantedVideoSubtypes []string
}

// CategoryCounters recomputes works, works_wanted, files, files_offline, parts and artists per category. Files group library_path by root; the others read the root-or-desired union with one distinct count per scope, since summing the two sides would double-count; parts and artists back ADR-54's level counts.
type CategoryCounters struct {
	store *store.Store
	clock clock.Clock
}

// NewCategoryCounters builds one over an open library store.
func NewCategoryCounters(opened *store.Store, cl clock.Clock) CategoryCounters {
	return CategoryCounters{store: opened, clock: cl}
}

// Reconcile recomputes "works" and "files" for each scope and writes back any
// counter that does not already match, returning every correction made.
func (c CategoryCounters) Reconcile(ctx context.Context, scopes []CategoryScope) ([]Correction, error) {
	reader := librarydb.New(c.store.Read())

	fileRows, err := reader.CountLibraryPathsByRootFolder(ctx)
	if err != nil {
		return nil, fmt.Errorf("library: category counters: %w", err)
	}
	filesByRoot := make(map[int64]int64, len(fileRows))
	for _, row := range fileRows {
		filesByRoot[row.RootFolderID] = row.Total
	}

	now := c.clock.Now().Unix()
	var corrections []Correction
	for _, scope := range scopes {
		found, err := c.reconcileScope(ctx, reader, now, scope, filesByRoot)
		if err != nil {
			return nil, fmt.Errorf("library: category counters: reconcile category:%d: %w", scope.CategoryID, err)
		}
		corrections = append(corrections, found...)
	}
	found, err := c.reconcileLibrary(ctx, reader, now, filesByRoot)
	if err != nil {
		return nil, fmt.Errorf("library: category counters: reconcile library: %w", err)
	}
	return append(corrections, found...), nil
}

// reconcileLibrary keeps the whole library's figures the dashboard shows, in the scope it reads.
func (c CategoryCounters) reconcileLibrary(ctx context.Context, reader *librarydb.Queries, now int64, filesByRoot map[int64]int64) ([]Correction, error) {
	works, err := reader.CountAllWorks(ctx)
	if err != nil {
		return nil, fmt.Errorf("count works: %w", err)
	}
	states, err := reader.CountDesiredItemsByState(ctx, 0)
	if err != nil {
		return nil, fmt.Errorf("count wanted items: %w", err)
	}
	var files, monitored, missing int64
	for _, total := range filesByRoot {
		files += total
	}
	for _, row := range states {
		monitored += row.Total
		if row.State == "MISSING" || row.State == "PARTIAL" {
			missing += row.Total
		}
	}
	return c.writeScope(ctx, reader, now, "library", map[string]int64{"works": works, "files": files, "monitored": monitored, "missing": missing})
}

func (c CategoryCounters) reconcileScope(
	ctx context.Context, reader *librarydb.Queries, now int64, scope CategoryScope, filesByRoot map[int64]int64,
) ([]Correction, error) {
	scopeKey := fmt.Sprintf("category:%d", scope.CategoryID)

	encodedRootFolderIDs, err := json.Marshal(scope.RootFolderIDs)
	if err != nil {
		return nil, fmt.Errorf("encode root folder ids: %w", err)
	}
	works, err := reader.CountWorksVisibleToCategory(ctx, librarydb.CountWorksVisibleToCategoryParams{
		RootFolderIds: string(encodedRootFolderIDs), CategoryID: scope.CategoryID, MediaType: scope.MediaType,
	})
	if err != nil {
		return nil, fmt.Errorf("count works: %w", err)
	}
	parts, err := reader.CountWorkPartsVisibleToCategory(ctx, librarydb.CountWorkPartsVisibleToCategoryParams{
		RootFolderIds: string(encodedRootFolderIDs), CategoryID: scope.CategoryID, MediaType: scope.MediaType,
	})
	if err != nil {
		return nil, fmt.Errorf("count parts: %w", err)
	}
	wantedVideos := WantedVideos{Subtypes: scope.WantedVideoSubtypes}
	_, videoSubtypes, err := wantedVideos.encoded()
	if err != nil {
		return nil, err
	}
	wanted, err := reader.CountWantedWorksVisibleToCategory(ctx, librarydb.CountWantedWorksVisibleToCategoryParams{
		RootFolderIds: string(encodedRootFolderIDs), CategoryID: scope.CategoryID, MediaType: scope.MediaType,
		WantsVideos: boolToInt(len(scope.WantedVideoSubtypes) > 0), VideoSubtypes: videoSubtypes,
	})
	if err != nil {
		return nil, fmt.Errorf("count wanted works: %w", err)
	}
	artists, err := reader.CountArtistCreditsVisibleToCategory(ctx, librarydb.CountArtistCreditsVisibleToCategoryParams{
		RootFolderIds: string(encodedRootFolderIDs), CategoryID: scope.CategoryID, MediaType: scope.MediaType,
	})
	if err != nil {
		return nil, fmt.Errorf("count artists: %w", err)
	}
	subtypes, err := reader.CountWorksBySubtypeVisibleToCategory(ctx, librarydb.CountWorksBySubtypeVisibleToCategoryParams{
		RootFolderIds: string(encodedRootFolderIDs), CategoryID: scope.CategoryID, MediaType: scope.MediaType,
	})
	if err != nil {
		return nil, fmt.Errorf("count works by subtype: %w", err)
	}
	truth := map[string]int64{
		"works":         works,
		"works_wanted":  wanted,
		"files":         sumFor(filesByRoot, scope.RootFolderIDs),
		"files_offline": sumFor(filesByRoot, scope.OfflineRootFolderIDs),
		"parts":         parts,
		"artists":       artists,
	}
	for _, row := range subtypes {
		truth[SubtypeMetric(row.Subtype)] = row.Total
	}

	return c.writeScope(ctx, reader, now, scopeKey, truth)
}

// writeScope stores the figures of one counter scope that differ from what is stored and answers each correction.
func (c CategoryCounters) writeScope(ctx context.Context, reader *librarydb.Queries, now int64, scopeKey string, truth map[string]int64) ([]Correction, error) {
	existingRows, err := reader.ListCountersForScope(ctx, scopeKey)
	if err != nil {
		return nil, err
	}
	stored := make(map[string]int64, len(existingRows))
	for _, row := range existingRows {
		stored[row.Metric] = row.Value
	}

	// A subtype that no longer has a work is zeroed rather than left at its old figure.
	for metric := range stored {
		if _, current := truth[metric]; !current && strings.HasPrefix(metric, subtypeMetricPrefix) {
			truth[metric] = 0
		}
	}

	var corrections []Correction
	err = c.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		writer := librarydb.New(tx)
		for metric, value := range truth {
			was, wasSet := stored[metric]
			if wasSet && was == value {
				continue
			}
			if err := writer.SetCounter(ctx, librarydb.SetCounterParams{
				Scope: scopeKey, Metric: metric, Value: value, UpdatedAt: now,
			}); err != nil {
				return err
			}
			corrections = append(corrections, Correction{
				Scope: scopeKey, Metric: metric, Was: was, WasSet: wasSet, Now: value,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return corrections, nil
}

const subtypeMetricPrefix = "works.subtype."

// SubtypeMetric is the counter holding how many works of one subtype a category lists.
func SubtypeMetric(subtype string) string { return subtypeMetricPrefix + subtype }

func sumFor(byRoot map[int64]int64, rootFolderIDs []int64) int64 {
	var total int64
	for _, id := range rootFolderIDs {
		total += byRoot[id]
	}
	return total
}
