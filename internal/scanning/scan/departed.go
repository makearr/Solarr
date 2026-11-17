package scan

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/settings"
)

// departures marks the files a directory that was just listed in full no longer holds; a directory read completely is
// evidence about its own files and nothing else (section 10.4).
type departures struct {
	job    *Job
	root   settings.RootFolder
	marked int
	at     time.Time
	// refused is set when one run would mark more than massVanishFloor files: a person decides, a full scan can.
	refused bool
}

func (d *departures) read(ctx context.Context, directory string, listed []string) error {
	if d.refused || !d.root.Online {
		return nil
	}
	gone, err := d.job.physical.PathsGoneFromDirectory(ctx, d.root.ID, directory, listed)
	if err != nil || len(gone) == 0 {
		return err
	}
	if d.marked+len(gone) > massVanishFloor {
		d.refused = true
		d.job.logger.Error("folders changed on disk lost more files than a scan of changed folders will mark missing; "+
			"run a full scan of the root folder to settle it",
			slog.Int64("root_folder_id", d.root.ID), slog.Int("files", d.marked+len(gone)))
		return nil
	}
	if d.at.IsZero() {
		d.at = d.job.clock.Now()
	}
	marked, err := d.job.physical.MarkPathsMissing(ctx, d.root.ID, gone, d.at)
	d.marked += marked
	return err
}

// vanished handles a directory that no longer exists: its files are gone, unless the whole root looks unmounted.
func (d *departures) vanished(ctx context.Context, directory string) error {
	if directory == "" || (!strings.Contains(directory, "/") && !d.job.sweeper.HasEntries(d.root.Path)) {
		return nil
	}
	return d.read(ctx, directory, nil)
}

// finish runs the media type's housekeeping for what was marked, as the full sweep does.
func (d *departures) finish(ctx context.Context, sweep Sweep, categoryID int64) {
	if d.marked == 0 {
		return
	}
	d.job.logger.Info("library paths marked missing",
		slog.Int64("root_folder_id", d.root.ID), slog.Int("marked", d.marked))
	if sweep.Housekeeping == nil {
		return
	}
	outcome := SweepOutcome{MissingSince: d.at}
	if err := sweep.Housekeeping.AfterSweep(ctx, d.root, categoryID, outcome); err != nil {
		d.job.logger.Warn("scan housekeeping failed", slog.Int64("root_folder_id", d.root.ID), slog.Any("error", err))
	}
}
