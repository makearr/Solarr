package adapters

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/scanning/scan"
)

// VanishedFiles implements apiserver.VanishedFiles over scan_state and the paths it holds back.
type VanishedFiles struct {
	Scans    library.Scans
	Physical library.Physical
	Sweeps   scan.Sweeps
	Cache    *settings.Cache
	Logger   *slog.Logger
}

// Held implements apiserver.VanishedFiles.
func (v VanishedFiles) Held(ctx context.Context) (map[int64]int64, error) {
	all, err := v.Scans.All(ctx)
	if err != nil {
		return nil, err
	}
	held := map[int64]int64{}
	for _, progress := range all {
		if progress.VanishedHeld > 0 {
			held[progress.RootFolderID] = progress.VanishedHeld
		}
	}
	return held, nil
}

// Accept implements apiserver.VanishedFiles: marked as that scan would have, then each library's own file management
// (Unmonitor deleted) runs as after a sweep.
func (v VanishedFiles) Accept(ctx context.Context, rootFolderID int64) (int, error) {
	progress, err := v.Scans.Progress(ctx, rootFolderID)
	if err != nil {
		return 0, err
	}
	if progress.VanishedHeld == 0 {
		return 0, apiserver.ErrNothingHeld
	}
	marked, err := v.Physical.MarkMissing(ctx, rootFolderID, progress.VanishedThrough)
	if err != nil {
		return marked, err
	}
	if err := v.Scans.ClearVanished(ctx, rootFolderID); err != nil {
		return marked, err
	}
	snapshot := v.Cache.Snapshot()
	root, found := snapshot.RootFolder(rootFolderID)
	if !found {
		return marked, fmt.Errorf("adapters: root folder %d is gone", rootFolderID)
	}
	outcome := scan.SweepOutcome{MissingSince: progress.VanishedThrough}
	for _, categoryID := range snapshot.CategoriesForRootFolder(rootFolderID) {
		sweep, found := v.Sweeps.SweepFor(categoryID)
		if !found || sweep.Housekeeping == nil {
			continue
		}
		if err := sweep.Housekeeping.AfterSweep(ctx, root, categoryID, outcome); err != nil {
			v.Logger.Warn("housekeeping after accepting vanished files failed",
				slog.Int64("root_folder_id", rootFolderID), slog.Int64("category_id", categoryID), slog.Any("error", err))
		}
	}
	return marked, nil
}
