package adapters

import (
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
)

// LibraryLookup finds a library by id; adapters.Categories is the real one.
type LibraryLookup interface {
	Category(id int64) (settings.Category, bool)
}

// recyclingFor is the recycling bin of the libraries a file belongs to: the longest retention,
// and empty folders removed only when every one of them says so. None means the defaults.
func recyclingFor(libraries LibraryLookup, categoryIDs []int64) library.Recycling {
	var found []settings.FileManagement
	for _, id := range categoryIDs {
		if libraries == nil {
			break
		}
		if category, ok := libraries.Category(id); ok {
			found = append(found, category.FileManagement)
		}
	}
	if len(found) == 0 {
		found = []settings.FileManagement{settings.DefaultFileManagement}
	}
	recycling := library.Recycling{RemoveEmptyFolders: true}
	for _, fileManagement := range found {
		retain := time.Duration(fileManagement.RecycleRetentionDays) * 24 * time.Hour
		recycling.Retain = max(recycling.Retain, retain)
		recycling.RemoveEmptyFolders = recycling.RemoveEmptyFolders && fileManagement.DeleteEmptyFolders
	}
	return recycling
}
