package library

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/makearr/MediaNexus/internal/db/store"
	"github.com/makearr/MediaNexus/internal/db/store/gen/librarydb"
)

// dirtyBatchSize bounds each transaction, because a restored library can hold every
// one of ADR-22's 500k rows and Invariant 2 caps a transaction at 50 ms.
const dirtyBatchSize = 200

// Restored is the half of ADR-15's post-restore reconciliation that library.db owns.
type Restored struct{ store *store.Store }

// NewRestored builds it over an open library store.
func NewRestored(opened *store.Store) Restored { return Restored{store: opened} }

// ReferenceCount is how many library rows point at one settings.db id.
type ReferenceCount struct {
	ID   int64
	Rows int64
}

// MarkEverythingDirty puts every desired item back in the planner's dirty set
// (ADR-6): a restored library is state the planner has never seen. It returns how
// many it moved, which is what the notification reports.
func (r Restored) MarkEverythingDirty(ctx context.Context) (int64, error) {
	var total int64
	for {
		var changed int64
		err := r.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			var batchErr error
			changed, batchErr = librarydb.New(tx).MarkDesiredItemsDirtyBatch(ctx, dirtyBatchSize)
			return batchErr
		})
		if err != nil {
			return total, fmt.Errorf("library: mark everything dirty: %w", err)
		}
		// Each batch narrows what the next one matches, so a batch that changes
		// nothing means nothing is left clean.
		if changed == 0 {
			return total, nil
		}
		total += changed
	}
}

// CategoryReferences reports every category_id desired_item points at, with a row
// count. library.db holds no foreign key to settings.db (ADR-25), so finding the
// ones a restore orphaned is the application's job and needs both sides.
func (r Restored) CategoryReferences(ctx context.Context) ([]ReferenceCount, error) {
	rows, err := librarydb.New(r.store.Read()).CountDesiredItemsByCategory(ctx)
	if err != nil {
		return nil, fmt.Errorf("library: count desired items by category: %w", err)
	}
	counts := make([]ReferenceCount, len(rows))
	for index, row := range rows {
		counts[index] = ReferenceCount{ID: row.CategoryID, Rows: row.Total}
	}
	return counts, nil
}

// RootFolderReferences is the library_path.root_folder_id half of the same check.
func (r Restored) RootFolderReferences(ctx context.Context) ([]ReferenceCount, error) {
	rows, err := librarydb.New(r.store.Read()).CountLibraryPathsByRootFolder(ctx)
	if err != nil {
		return nil, fmt.Errorf("library: count library paths by root folder: %w", err)
	}
	counts := make([]ReferenceCount, len(rows))
	for index, row := range rows {
		counts[index] = ReferenceCount{ID: row.RootFolderID, Rows: row.Total}
	}
	return counts, nil
}
