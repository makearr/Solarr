// Package housekeeping is the File Management a sweep applies the same way for every type that wants whole works or
// parts of them: Sonarr's and Radarr's "Unmonitor Deleted" and "Delete Empty Folders".
package housekeeping

import (
	"context"
	"log/slog"
	"path"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/scanning/scan"
)

// Sweep is what a full sweep applies when its library asks: Unmonitor Deleted at the grain the type wants, and
// Delete Empty Folders for folders emptied outside Solarr (Solarr's own deletes go through the recycle bin).
type Sweep struct {
	Categories interface {
		Category(id int64) (settings.Category, bool)
	}
	Desired library.Desired
	Grain   media.WantedGrain
	FS      fsx.FS
	Logger  *slog.Logger
}

var _ scan.Housekeeping = Sweep{}

// AfterSweep implements scan.Housekeeping: an episode or movie whose files were deleted is unmonitored rather than
// left missing and fetched again, and the folders the deletion emptied go.
func (h Sweep) AfterSweep(ctx context.Context, root settings.RootFolder, categoryID int64, outcome scan.SweepOutcome) error {
	category, found := h.Categories.Category(categoryID)
	if !found {
		return nil
	}
	if category.FileManagement.DeleteEmptyFolders && h.FS != nil {
		h.deleteEmptyFolders(root.Path, outcome.EmptyDirectories)
	}
	if !category.FileManagement.UnmonitorDeleted || outcome.MissingSince.IsZero() {
		return nil
	}
	unmonitor := h.Desired.UnmonitorWorksLosingFiles
	if h.Grain == media.WantedParts {
		unmonitor = h.Desired.UnmonitorPartsLosingFiles
	}
	unmonitored, err := unmonitor(ctx, categoryID, root.ID, outcome.MissingSince.Unix())
	if unmonitored > 0 && h.Logger != nil {
		h.Logger.Info("wanted items whose files were deleted are no longer monitored",
			slog.Int64("root_folder_id", root.ID), slog.Int("unmonitored", unmonitored))
	}
	return err
}

// deleteEmptyFolders removes each empty folder and the parents it leaves empty, never the root itself. Remove refuses
// a folder that is no longer empty, so a file arriving since the walk stops it there.
func (h Sweep) deleteEmptyFolders(rootPath string, empty []string) {
	removed := 0
	for _, relative := range empty {
		for folder := relative; folder != "." && folder != "" && folder != "/"; folder = path.Dir(folder) {
			if err := h.FS.Remove(path.Join(rootPath, folder)); err != nil {
				break
			}
			removed++
		}
	}
	if removed > 0 && h.Logger != nil {
		h.Logger.Info("empty folders deleted", slog.String("root", rootPath), slog.Int("folders", removed))
	}
}
