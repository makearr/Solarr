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
