package tv

import (
	"context"
	"log/slog"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/scanning/scan"
)

// Housekeeping applies a TV library's File Management after a sweep of one of its roots.
type Housekeeping struct {
	Categories interface {
		Category(id int64) (settings.Category, bool)
	}
	Desired library.Desired
	Logger  *slog.Logger
}

var _ scan.Housekeeping = Housekeeping{}

// AfterSweep implements scan.Housekeeping: with "Unmonitor deleted" on, an episode whose file a full sweep found gone
// is unmonitored rather than left missing and fetched again.
func (h Housekeeping) AfterSweep(ctx context.Context, root settings.RootFolder, categoryID int64, outcome scan.SweepOutcome) error {
	category, found := h.Categories.Category(categoryID)
	if !found || !category.FileManagement.UnmonitorDeleted || outcome.MissingSince.IsZero() {
		return nil
	}
	unmonitored, err := h.Desired.UnmonitorPartsLosingFiles(ctx, categoryID, root.ID, outcome.MissingSince.Unix())
	if unmonitored > 0 && h.Logger != nil {
		h.Logger.Info("episodes whose files were deleted are no longer monitored",
			slog.Int64("root_folder_id", root.ID), slog.Int("episodes", unmonitored))
	}
	return err
}
