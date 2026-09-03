package library

import (
	"context"
	"database/sql"
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

// CategoryCounters recomputes "works" and "files" per category from
// work_root_folder and library_path -- the same temporary bridge (migration 0012)
// the works list and search use, standing in for desired_item until it exists for
// music. Unlike Reconciler's desired-item-state metrics, neither source table needs
// a per-category SQL filter: both are grouped by root_folder_id once, and a
// category's total is the sum over whichever roots it spans (drift-and-repair is
// the design, not a fallback -- see Reconciler's own doc comment).
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

	workRows, err := reader.CountWorksByRootFolder(ctx)
	if err != nil {
		return nil, fmt.Errorf("library: category counters: %w", err)
	}
	worksByRoot := make(map[int64]int64, len(workRows))
	for _, row := range workRows {
		worksByRoot[row.RootFolderID] = row.Total
	}

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
		found, err := c.reconcileScope(ctx, now, scope, worksByRoot, filesByRoot)
		if err != nil {
			return nil, fmt.Errorf("library: category counters: reconcile category:%d: %w", scope.CategoryID, err)
		}
		corrections = append(corrections, found...)
	}
	return corrections, nil
}

func (c CategoryCounters) reconcileScope(
	ctx context.Context, now int64, scope CategoryScope, worksByRoot, filesByRoot map[int64]int64,
) ([]Correction, error) {
	scopeKey := fmt.Sprintf("category:%d", scope.CategoryID)
	truth := map[string]int64{
		"works": sumFor(worksByRoot, scope.RootFolderIDs),
		"files": sumFor(filesByRoot, scope.RootFolderIDs),
	}

	existingRows, err := librarydb.New(c.store.Read()).ListCountersForScope(ctx, scopeKey)
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
