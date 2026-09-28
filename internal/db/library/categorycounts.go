package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// CategoryScope names one category and the root folders it spans -- settings.db
// policy (ADR-25), so the caller supplies it, the same shape as ReconcileScope.
type CategoryScope struct {
	CategoryID    int64
	RootFolderIDs []int64
	// OfflineRootFolderIDs is the subset of RootFolderIDs settings.db currently
	// declares offline (section 10.4) -- also the caller's own policy fact,
	// composed in Go the same way RootFolderIDs itself is (ADR-25). Backs
	// "a category with an offline root reports its full count with an
	// offline note" (todo 4.18): "files" stays the true total regardless of
	// reachability, "files_offline" is how many of those sit on a root that
	// is not reachable right now.
	OfflineRootFolderIDs []int64
}

// CategoryCounters recomputes "works", "works_wanted", "files", "files_offline", "parts" and
// "artists" per category. "files" is library_path grouped by root folder --
// a file only ever exists on disk, under a root folder, so that half needs
// no desired_item involvement; "files_offline" is the same grouping summed
// over only the scope's own offline roots (todo 4.18). "works", "parts" and
// "artists" all read the same on-disk-or-desired union (work_root_folder,
// migration 0012's bridge, OR desired_item.category_id for a subscription-
// discovered work with no root folder yet) -- summing the two sides
// separately would double-count a scanned, monitored work that has rows on
// both, so each is one distinct-count query per scope rather than a Go-side
// sum over a bulk grouped query the way "files"/"files_offline" are.
// "parts" and "artists" back ADR-54's navigation-level counts (work_part-
// entity and person-entity levels respectively, e.g. music's Tracks and
// Artists).
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
	return corrections, nil
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
		RootFolderIds: string(encodedRootFolderIDs), CategoryID: scope.CategoryID,
	})
	if err != nil {
		return nil, fmt.Errorf("count works: %w", err)
	}
	parts, err := reader.CountWorkPartsVisibleToCategory(ctx, librarydb.CountWorkPartsVisibleToCategoryParams{
		RootFolderIds: string(encodedRootFolderIDs), CategoryID: scope.CategoryID,
	})
	if err != nil {
		return nil, fmt.Errorf("count parts: %w", err)
	}
	wanted, err := reader.CountWantedWorksVisibleToCategory(ctx, librarydb.CountWantedWorksVisibleToCategoryParams{
		RootFolderIds: string(encodedRootFolderIDs), CategoryID: scope.CategoryID,
	})
	if err != nil {
		return nil, fmt.Errorf("count wanted works: %w", err)
	}
	artists, err := reader.CountArtistCreditsVisibleToCategory(ctx, librarydb.CountArtistCreditsVisibleToCategoryParams{
		RootFolderIds: string(encodedRootFolderIDs), CategoryID: scope.CategoryID,
	})
	if err != nil {
		return nil, fmt.Errorf("count artists: %w", err)
	}
	truth := map[string]int64{
		"works":         works,
		"works_wanted":  wanted,
		"files":         sumFor(filesByRoot, scope.RootFolderIDs),
		"files_offline": sumFor(filesByRoot, scope.OfflineRootFolderIDs),
		"parts":         parts,
		"artists":       artists,
	}

	existingRows, err := reader.ListCountersForScope(ctx, scopeKey)
	if err != nil {
		return nil, err
	}
	stored := make(map[string]int64, len(existingRows))
	for _, row := range existingRows {
		stored[row.Metric] = row.Value
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

func sumFor(byRoot map[int64]int64, rootFolderIDs []int64) int64 {
	var total int64
	for _, id := range rootFolderIDs {
		total += byRoot[id]
	}
	return total
}
