package works

import (
	"context"
	"fmt"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// Files is a work-without-parts type's media.PlacingFiles: a queued file becomes a file of the work a person names,
// and with it, when asked or when the type's work is a folder, every other queued file beside it.
type Files struct {
	// Resolve finds or adds the work a placement names; 0 when it could not.
	Resolve      func(ctx context.Context, categoryID int64, placement media.Placement) (int64, error)
	Series       library.Series
	Unidentified library.Unidentified
	Clock        clock.Clock
	// WholeFolder places a file's folder with it always: an audiobook's chapters.
	WholeFolder bool
	// AllowAgain lifts the exclusion of a work a person names on purpose; nil lifts nothing.
	AllowAgain func(ctx context.Context, provider, externalID string) error
}

var _ media.PlacingFiles = Files{}

// PlaceFile implements media.PlacingFiles.
func (f Files) PlaceFile(ctx context.Context, categoryID, mediaObjectID int64, placement media.Placement, wholeFolder bool) (int64, int64, error) {
	if f.AllowAgain != nil {
		if err := f.AllowAgain(ctx, placement.Provider, placement.ExternalID); err != nil {
			return 0, 0, err
		}
	}
	workID, err := f.Resolve(ctx, categoryID, placement)
	if err != nil {
		return 0, 0, err
	}
	if workID <= 0 {
		return 0, 0, fmt.Errorf("%w: that work could not be added", media.ErrInvalidAdd)
	}
	entry, err := f.Unidentified.Get(ctx, mediaObjectID)
	if err != nil {
		return 0, 0, err
	}
	files := []int64{mediaObjectID}
	if wholeFolder || f.WholeFolder {
		if files, err = f.Unidentified.InFolder(ctx, entry.RootFolderID, entry.RelPath); err != nil {
			return 0, 0, err
		}
	}
	now := f.Clock.Now()
	if _, err := f.Series.AttachWholeFiles(ctx, now, workID, entry.RootFolderID, files); err != nil {
		return workID, 0, err
	}
	for _, id := range files {
		if err := f.Unidentified.RecordManual(ctx, id, workID, now.Unix()); err != nil {
			return workID, 0, err
		}
	}
	if err := f.Unidentified.Clear(ctx, files); err != nil {
		return workID, 0, err
	}
	return workID, int64(len(files)), nil
}
