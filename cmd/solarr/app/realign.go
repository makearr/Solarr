package app

import (
	"context"
	"errors"
	"log/slog"
	"path"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/scanning/scan"
)

// realigner is music.Realigner: it rescans the directory each refined work's
// file lives in, so the scan matches those files against the tracklist that
// now exists. One scoped scan per root, never a rescan of the whole library.
type realigner struct{ p *process }

func (r realigner) RealignWorks(ctx context.Context, workIDs []int64) error {
	locations, err := library.NewIdentity(r.p.library).WorkFileLocations(ctx, workIDs)
	if err != nil {
		return err
	}

	directoriesByRoot := make(map[int64][]string)
	for _, location := range locations {
		directoriesByRoot[location.RootFolderID] = append(directoriesByRoot[location.RootFolderID], path.Dir(location.RelPath))
	}

	var errs []error
	for rootFolderID, directories := range directoriesByRoot {
		categoryID, found := r.musicCategoryFor(rootFolderID)
		if !found {
			continue
		}
		if _, err := scan.EnqueueDirectories(ctx, r.p.queue, rootFolderID, categoryID, directories); err != nil {
			errs = append(errs, err)
			continue
		}
		r.p.logger.Info("rescan queued for refined albums",
			slog.Int64("root_folder_id", rootFolderID), slog.Int("directories", len(directories)))
	}
	return errors.Join(errs...)
}

// musicCategoryFor is the music category a root belongs to, the same one an
// ordinary scan of that root is enqueued under (enqueueScans).
func (r realigner) musicCategoryFor(rootFolderID int64) (int64, bool) {
	snapshot := r.p.cache.Snapshot()
	for _, category := range snapshot.Categories() {
		if category.MediaType != "music" {
			continue
		}
		for _, root := range snapshot.RootFoldersForCategory(category.ID) {
			if root.ID == rootFolderID && root.Online {
				return category.ID, true
			}
		}
	}
	return 0, false
}
