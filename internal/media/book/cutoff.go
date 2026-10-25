package book

import (
	"context"
	"log/slog"

	"github.com/makearr/Solarr/internal/acquiring/cutoff"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media/works"
)

// Item states the cutoff check and the search move a book between.
const (
	ItemStateUpgradable = works.StateUpgradable
	ItemStateComplete   = works.StateComplete
)

// CutoffJob is the format's specialisation of the shared cutoff check: an ebook's files are versions of it, judged by
// their format; an audiobook's chapters are one set, never contested and never upgraded.
type CutoffJob struct {
	Format   Format
	Desired  library.Desired
	Reader   library.SeriesReader
	Physical library.Physical
	Settings *settings.Cache
	// Recycle moves a file to the recycle bin; replaced is true for an owned file an upgrade superseded. Nil leaves
	// every file in place.
	Recycle func(ctx context.Context, mediaObjectID, categoryID int64, replaced bool) error
	Logger  *slog.Logger
}

func (j CutoffJob) shared() works.Cutoff {
	return works.Cutoff{
		Desired: j.Desired, Recycle: j.Recycle, Versions: true, Uncontested: j.Format.ByFolder, NeverUpgrades: j.Format.ByFolder,
		Page: func(ctx context.Context, afterID, limit int64) ([]library.CutoffItem, error) {
			return j.Desired.CutoffPageOf(ctx, j.Format.MediaType, afterID, limit)
		},
		Files: func(ctx context.Context, itemID int64) ([]works.File, error) {
			return fileFacts(ctx, j.Format, j.Reader, j.Physical, itemID)
		},
	}
}

// CutoffPage implements cutoff.Check.
func (j CutoffJob) CutoffPage(ctx context.Context, workID, afterID, limit int64) ([]library.CutoffItem, error) {
	return j.shared().CutoffPage(ctx, workID, afterID, limit)
}

// CheckCutoff implements cutoff.Check.
func (j CutoffJob) CheckCutoff(ctx context.Context, snapshot *settings.Snapshot, item library.CutoffItem) (cutoff.Outcome, error) {
	return j.shared().CheckCutoff(ctx, snapshot, item)
}
