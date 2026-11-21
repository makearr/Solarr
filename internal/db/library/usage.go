package library

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// Usage answers what in library.db still names a settings row, so settings can refuse to delete it (DB-16).
type Usage struct{ store *store.Store }

// NewUsage builds it over an open library store.
func NewUsage(opened *store.Store) Usage { return Usage{store: opened} }

// RootFolderRows counts what keeps a root folder: files the library holds on it, recycle bin entries and files waiting
// to be identified.
func (u Usage) RootFolderRows(ctx context.Context, rootFolderID int64) (files, recycled, queued int64, err error) {
	queries := librarydb.New(u.store.Read())
	holds, err := queries.RootHoldsFiles(ctx, rootFolderID)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("library: files on root %d: %w", rootFolderID, err)
	}
	if recycled, err = queries.CountRootRecycleEntries(ctx, sql.NullInt64{Int64: rootFolderID, Valid: true}); err != nil {
		return 0, 0, 0, fmt.Errorf("library: recycle bin of root %d: %w", rootFolderID, err)
	}
	if queued, err = queries.CountRootIdentificationQueue(ctx, rootFolderID); err != nil {
		return 0, 0, 0, fmt.Errorf("library: identification queue of root %d: %w", rootFolderID, err)
	}
	return holds, recycled, queued, nil
}

// CategoryFilesOnRoot counts the files of a library's items that sit on one root folder.
func (u Usage) CategoryFilesOnRoot(ctx context.Context, categoryID, rootFolderID int64) (int64, error) {
	count, err := librarydb.New(u.store.Read()).CountCategoryFilesOnRoot(ctx, librarydb.CountCategoryFilesOnRootParams{
		RootFolderID: rootFolderID, CategoryID: categoryID,
	})
	if err != nil {
		return 0, fmt.Errorf("library: files of library %d on root %d: %w", categoryID, rootFolderID, err)
	}
	return count, nil
}

// ItemsPinnedToProfile counts the items that name a quality profile, by their stored default or their own choice.
func (u Usage) ItemsPinnedToProfile(ctx context.Context, profileID int64) (int64, error) {
	count, err := librarydb.New(u.store.Read()).CountItemsPinnedToProfile(ctx, profileID)
	if err != nil {
		return 0, fmt.Errorf("library: items on quality profile %d: %w", profileID, err)
	}
	return count, nil
}

// ItemsPinnedToTarget counts the items that chose a quality target of their own.
func (u Usage) ItemsPinnedToTarget(ctx context.Context, targetID int64) (int64, error) {
	count, err := librarydb.New(u.store.Read()).CountItemsPinnedToTarget(ctx, targetID)
	if err != nil {
		return 0, fmt.Errorf("library: items on quality target %d: %w", targetID, err)
	}
	return count, nil
}

// RepointProfile moves every item that names one quality profile, by stored default or own choice, onto another, in chunks of 200.
func (u Usage) RepointProfile(ctx context.Context, from, to int64) error {
	return u.repoint(ctx, fmt.Sprintf("quality profile %d", from), func(ctx context.Context, queries *librarydb.Queries) (int64, error) {
		stored, err := queries.RepointItemProfileChunk(ctx, librarydb.RepointItemProfileChunkParams{ToID: to, FromID: from})
		if err != nil {
			return 0, err
		}
		own, err := queries.RepointProfileOverrideChunk(ctx, librarydb.RepointProfileOverrideChunkParams{ToID: to, FromID: from})
		return stored + own, err
	})
}

// RepointTarget moves every item that chose one quality target onto another, in chunks of 200.
func (u Usage) RepointTarget(ctx context.Context, from, to int64) error {
	return u.repoint(ctx, fmt.Sprintf("quality target %d", from), func(ctx context.Context, queries *librarydb.Queries) (int64, error) {
		return queries.RepointTargetOverrideChunk(ctx, librarydb.RepointTargetOverrideChunkParams{ToID: to, FromID: from})
	})
}

func (u Usage) repoint(ctx context.Context, what string, chunk func(context.Context, *librarydb.Queries) (int64, error)) error {
	for {
		var moved int64
		err := u.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			var err error
			moved, err = chunk(ctx, librarydb.New(tx))
			return err
		})
		if err != nil {
			return fmt.Errorf("library: repoint items of %s: %w", what, err)
		}
		if moved == 0 {
			return nil
		}
	}
}
