// Package housekeeping is the File Management a sweep applies the same way for every type that wants whole works or
// parts of them: Sonarr's and Radarr's "Unmonitor Deleted".
package housekeeping

import (
	"context"
	"log/slog"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/scanning/scan"
)

// UnmonitorDeleted unmonitors what a full sweep found gone when its library asks, at the grain the type wants.
type UnmonitorDeleted struct {
	Categories interface {
		Category(id int64) (settings.Category, bool)
	}
	Desired library.Desired
	Grain   media.WantedGrain
	Logger  *slog.Logger
}

var _ scan.Housekeeping = UnmonitorDeleted{}

// AfterSweep implements scan.Housekeeping: an episode or movie whose files were deleted is unmonitored rather than
// left missing and fetched again.
func (h UnmonitorDeleted) AfterSweep(ctx context.Context, root settings.RootFolder, categoryID int64, outcome scan.SweepOutcome) error {
	category, found := h.Categories.Category(categoryID)
	if !found || !category.FileManagement.UnmonitorDeleted || outcome.MissingSince.IsZero() {
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
