package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/internal/store/gen/librarydb"
)

// CategoryScope names one category and the root folders it spans -- settings.db
// policy (ADR-25), so the caller supplies it, the same shape as ReconcileScope.
type CategoryScope struct {
	CategoryID    int64
	RootFolderIDs []int64
}

// CategoryCounters recomputes "works" and "files" per category. "files" is
// library_path grouped by root folder -- a file only ever exists on disk, under
// a root folder, so that half needs no desired_item involvement. "works" is the
// union of work_root_folder (on-disk presence, migration 0012's bridge) and
// desired_item.category_id (a subscription-discovered work has no root folder
// yet) -- summing the two separately would double-count a scanned, monitored
// work that has rows on both sides, so "works" is one distinct-count query per
// scope rather than a Go-side sum over a bulk grouped query the way "files" is.
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
	truth := map[string]int64{
		"works": works,
		"files": sumFor(filesByRoot, scope.RootFolderIDs),
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
