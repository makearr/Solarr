package adapters

import (
	"context"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
)

// EntityLibraries answers which library of MediaType a work or person is looked up for (music.Libraries): a work's first
// that wants it, else that holds its files; a person's first holding their works. Unreadable is 0, the default order.
type EntityLibraries struct {
	MediaType string
	Works     library.Works
	People    library.People
	Cache     *settings.Cache
}

// OfWork implements music.Libraries.
func (e EntityLibraries) OfWork(ctx context.Context, workID int64) int64 {
	rootFolderIDs, categoryIDs, err := e.Works.Places(ctx, workID)
	if err != nil {
		return 0
	}
	snapshot := e.Cache.Snapshot()
	if categoryID := e.first(snapshot, categoryIDs); categoryID != 0 {
		return categoryID
	}
	for _, rootFolderID := range rootFolderIDs {
		if categoryID := e.first(snapshot, snapshot.CategoriesForRootFolder(rootFolderID)); categoryID != 0 {
			return categoryID
		}
	}
	return 0
}

// OfPerson implements music.Libraries.
func (e EntityLibraries) OfPerson(ctx context.Context, personID int64) int64 {
	categoryIDs, err := e.People.LibrariesOf(ctx, personID)
	if err != nil {
		return 0
	}
	return e.first(e.Cache.Snapshot(), categoryIDs)
}

// first is the first of categoryIDs that is a library of MediaType, 0 for none.
func (e EntityLibraries) first(snapshot *settings.Snapshot, categoryIDs []int64) int64 {
	for _, categoryID := range categoryIDs {
		if category, found := snapshot.Category(categoryID); found && category.MediaType == e.MediaType {
			return categoryID
		}
	}
	return 0
}
