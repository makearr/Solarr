package movie

import (
	"context"
	"log/slog"

	"github.com/makearr/Solarr/internal/acquiring/cutoff"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media/works"
)

// CutoffJob is the movie specialisation of the shared cutoff check: a movie's files are versions of it, so two held
// on purpose are no contest and one at the cutoff completes it.
type CutoffJob struct {
	Desired  library.Desired
	Reader   library.SeriesReader
	Settings *settings.Cache
	// Recycle moves a file to the recycle bin; replaced is true for an owned file an upgrade superseded, false for a
	// download that measured no better. Nil leaves every file in place.
	Recycle func(ctx context.Context, mediaObjectID, categoryID int64, replaced bool) error
	Logger  *slog.Logger
}

func (j CutoffJob) shared() works.Cutoff {
	return works.VersionedCutoff(MediaType, j.Desired, j.Reader, j.Recycle)
}

// CutoffPage implements cutoff.Check.
func (j CutoffJob) CutoffPage(ctx context.Context, workID, afterID, limit int64) ([]library.CutoffItem, error) {
	return j.shared().CutoffPage(ctx, workID, afterID, limit)
}

// StateUnder implements cutoff.Previewing.
func (j CutoffJob) StateUnder(ctx context.Context, snapshot *settings.Snapshot, category settings.Category, item library.CutoffItem) (string, bool, error) {
	return j.shared().StateUnder(ctx, snapshot, category, item)
}

// CheckCutoff implements cutoff.Check.
func (j CutoffJob) CheckCutoff(ctx context.Context, snapshot *settings.Snapshot, item library.CutoffItem) (cutoff.Outcome, error) {
	return j.shared().CheckCutoff(ctx, snapshot, item)
}
