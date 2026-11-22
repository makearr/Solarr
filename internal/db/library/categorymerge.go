package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// mergeChunk bounds the wanted items one write moves, so no transaction holds the library for long.
const mergeChunk = 200

// CategoryMerges moves what the library holds for one category to another, the data half of merging two libraries.
type CategoryMerges struct{ store *store.Store }

// NewCategoryMerges builds it over an open library store.
func NewCategoryMerges(opened *store.Store) CategoryMerges { return CategoryMerges{store: opened} }

// Merge moves every wanted item, request, collection, session, move and follow from one category to another and recomputes counts; duplicates fold into the target's item, and a failed merge is finished by calling it again.
func (m CategoryMerges) Merge(ctx context.Context, from, into int64) (int64, error) {
	if from == into {
		return 0, fmt.Errorf("library: a library cannot be merged into itself")
	}
	var moved, after int64
	for {
		ids, err := librarydb.New(m.store.Read()).NextCategoryItemIDs(ctx, librarydb.NextCategoryItemIDsParams{CategoryID: from, ID: after, Limit: mergeChunk})
		if err != nil {
			return moved, fmt.Errorf("library: wanted items of library %d: %w", from, err)
		}
		if len(ids) == 0 {
			break
		}
		after = ids[len(ids)-1]
		if err := m.moveItems(ctx, ids, into); err != nil {
			return moved, err
		}
		moved += int64(len(ids))
	}
	err := m.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		nullable := func(id int64) sql.NullInt64 { return sql.NullInt64{Int64: id, Valid: true} }
		steps := []func() error{
			func() error {
				return queries.MoveRequestsToCategory(ctx, librarydb.MoveRequestsToCategoryParams{IntoCategoryID: nullable(into), FromCategoryID: nullable(from)})
			},
			func() error { return queries.DropCategoryRequests(ctx, nullable(from)) },
			func() error {
				return queries.MoveCollectionsToCategory(ctx, librarydb.MoveCollectionsToCategoryParams{IntoCategoryID: into, FromCategoryID: from})
			},
			func() error { return queries.DropCategoryCollections(ctx, from) },
			func() error {
				return queries.MoveImportSessionsToCategory(ctx, librarydb.MoveImportSessionsToCategoryParams{IntoCategoryID: nullable(into), FromCategoryID: nullable(from)})
			},
			func() error { return queries.DropCategoryImportSessions(ctx, nullable(from)) },
			func() error {
				return queries.MoveMoveJobsToCategory(ctx, librarydb.MoveMoveJobsToCategoryParams{IntoCategoryID: nullable(into), FromCategoryID: nullable(from)})
			},
			func() error { return queries.DropCategoryMoveJobs(ctx, nullable(from)) },
			func() error {
				return queries.MoveSubscriptionsToCategory(ctx, librarydb.MoveSubscriptionsToCategoryParams{IntoCategoryID: nullable(into), FromCategoryID: nullable(from)})
			},
			func() error { return queries.DropCategorySubscriptions(ctx, nullable(from)) },
			func() error { return queries.DropCategoryDesiredItems(ctx, from) },
			func() error { return queries.DropCategoryPeople(ctx, from) },
			func() error { return queries.DropCategoryOrganisations(ctx, from) },
		}
		for _, step := range steps {
			if err := step(); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return moved, fmt.Errorf("library: merge library %d into %d: %w", from, into, err)
	}
	return moved, nil
}

// moveItems re-files one chunk of wanted items and recomputes the people and organisations of their works. An item the
// target already holds for the same thing hands its files and downloads to that one first.
func (m CategoryMerges) moveItems(ctx context.Context, ids []int64, into int64) error {
	encoded, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	list := string(encoded)
	return m.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		duplicates, err := queries.ListMergeDuplicates(ctx, librarydb.ListMergeDuplicatesParams{IntoCategoryID: into, ItemIds: list})
		if err != nil {
			return fmt.Errorf("library: find duplicate wanted items: %w", err)
		}
		for _, duplicate := range duplicates {
			if err := queries.MergeDesiredItemFiles(ctx, librarydb.MergeDesiredItemFilesParams{
				ToItemID: duplicate.CounterpartID, FromItemID: duplicate.ItemID, KeepID: duplicate.WorkID,
			}); err != nil {
				return fmt.Errorf("library: fold files of item %d: %w", duplicate.ItemID, err)
			}
			if err := queries.MoveAcquisitionsToItem(ctx, librarydb.MoveAcquisitionsToItemParams{
				ToItemID: sql.NullInt64{Int64: duplicate.CounterpartID, Valid: true}, FromItemID: sql.NullInt64{Int64: duplicate.ItemID, Valid: true},
			}); err != nil {
				return fmt.Errorf("library: fold downloads of item %d: %w", duplicate.ItemID, err)
			}
			if err := queries.MarkDesiredItemDirty(ctx, duplicate.CounterpartID); err != nil {
				return err
			}
			if err := queries.DeleteDesiredItemRow(ctx, duplicate.ItemID); err != nil {
				return fmt.Errorf("library: drop duplicate wanted item %d: %w", duplicate.ItemID, err)
			}
		}
		// dirty: the planner works out what each now wants in its new library.
		if err := queries.MoveDesiredItemsToCategory(ctx, librarydb.MoveDesiredItemsToCategoryParams{IntoCategoryID: into, ItemIds: list}); err != nil {
			return fmt.Errorf("library: move wanted items: %w", err)
		}
		workIDs, err := queries.ListWorksOfItems(ctx, librarydb.ListWorksOfItemsParams{CategoryID: into, ItemIds: list})
		if err != nil {
			return err
		}
		for _, workID := range workIDs {
			if err := refreshWorkMembers(ctx, queries, workID); err != nil {
				return err
			}
		}
		return nil
	})
}
