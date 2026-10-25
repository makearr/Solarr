package tv

import (
	"context"
	"log/slog"

	"github.com/makearr/Solarr/internal/acquiring/cutoff"
	"github.com/makearr/Solarr/internal/acquiring/releasefacts"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/score"
	"github.com/makearr/Solarr/internal/media/works"
)

// Item states the check moves a season between.
const (
	ItemStateUpgradable = works.StateUpgradable
	ItemStateComplete   = works.StateComplete
)

// CutoffJob settles an episode a download brought a second file for (the strictly better file stays, a tie keeps the
// owned one), then marks each season UPGRADABLE or COMPLETE by what the probe measured.
type CutoffJob struct {
	Desired  library.Desired
	Series   library.SeriesReader
	Settings *settings.Cache
	// Recycle moves a file to the recycle bin; replaced is true for an owned file an upgrade superseded, false for a
	// download that measured no better. Nil leaves every file in place.
	Recycle func(ctx context.Context, mediaObjectID, categoryID int64, replaced bool) error
	Logger  *slog.Logger
}

func (j CutoffJob) shared() works.Cutoff {
	return works.Cutoff{
		Desired: j.Desired, Recycle: j.Recycle, Page: j.Desired.TVCutoffPage,
		Files: func(ctx context.Context, itemID int64) ([]works.File, error) {
			return seasonFiles(ctx, j.Series, itemID)
		},
	}
}

// CutoffPage implements cutoff.Check: a page of monitored seasons, or every season of the one series workID names.
func (j CutoffJob) CutoffPage(ctx context.Context, workID, afterID, limit int64) ([]library.CutoffItem, error) {
	return j.shared().CutoffPage(ctx, workID, afterID, limit)
}

// CheckCutoff implements cutoff.Check: a second file of an episode is settled whatever the season's state, a file
// holding several episodes only once it lost every one; a season with gaps is not marked, and one with an episode
// below the cutoff is UPGRADABLE.
func (j CutoffJob) CheckCutoff(ctx context.Context, snapshot *settings.Snapshot, item library.CutoffItem) (cutoff.Outcome, error) {
	return j.shared().CheckCutoff(ctx, snapshot, item)
}

// FileFacts is what the probe measured of an episode's file, as the facts a profile or a target judges.
func FileFacts(file library.EpisodeFile) score.Facts { return releasefacts.FromFile(file) }
