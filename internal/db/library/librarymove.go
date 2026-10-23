package library

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// MoveWorkToLibrary moves a work's items from one library to another, which they then belong to under profileID; an
// item the target already holds (an earlier copy) is dropped from the source instead. It answers how many moved.
func (d Desired) MoveWorkToLibrary(ctx context.Context, workID, fromCategoryID, toCategoryID, profileID int64) (int64, error) {
	var moved int64
	err := d.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		if err := queries.DropWorkItemsTheTargetHas(ctx, librarydb.DropWorkItemsTheTargetHasParams{
			WorkID: workID, FromCategoryID: fromCategoryID, ToCategoryID: toCategoryID,
		}); err != nil {
			return err
		}
		var err error
		moved, err = queries.MoveWorkItemsToCategory(ctx, librarydb.MoveWorkItemsToCategoryParams{
			ToCategoryID: toCategoryID, ProfileID: profileID, WorkID: workID, FromCategoryID: fromCategoryID,
		})
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("library: move work %d to library %d: %w", workID, toCategoryID, err)
	}
	return moved, nil
}

// CopyWorkToLibrary gives a work the same items in another library, unmonitored and covered by the same files; the
// items it already has there are left alone. It answers how many it added.
func (d Desired) CopyWorkToLibrary(ctx context.Context, workID, fromCategoryID, toCategoryID, profileID int64) (int64, error) {
	var copied int64
	err := d.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		var err error
		copied, err = queries.CopyWorkItemsToCategory(ctx, librarydb.CopyWorkItemsToCategoryParams{
			ToCategoryID: toCategoryID, ProfileID: profileID, WorkID: workID, FromCategoryID: fromCategoryID,
		})
		if err != nil {
			return err
		}
		return queries.CopyWorkItemFilesToCategory(ctx, librarydb.CopyWorkItemFilesToCategoryParams{
			ToCategoryID: toCategoryID, WorkID: workID, FromCategoryID: fromCategoryID,
		})
	})
	if err != nil {
		return 0, fmt.Errorf("library: copy work %d to library %d: %w", workID, toCategoryID, err)
	}
	return copied, nil
}
