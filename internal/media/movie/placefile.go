package movie

import (
	"context"
	"fmt"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// Files is the movie module's media.PlacingFiles: a queued file becomes a version of the movie a person names.
type Files struct {
	Resolver     Resolver
	Series       library.Series
	Unidentified library.Unidentified
	Clock        clock.Clock
	// AllowAgain lifts the exclusion of a movie a person names on purpose; nil lifts nothing.
	AllowAgain func(ctx context.Context, provider, externalID string) error
}

var _ media.PlacingFiles = Files{}

// PlaceFile implements media.PlacingFiles; with wholeFolder every other queued file beside it joins the same movie.
func (f Files) PlaceFile(ctx context.Context, categoryID, mediaObjectID int64, placement media.Placement, wholeFolder bool) (int64, int64, error) {
	ids, err := namedIDs(placement.Provider, placement.ExternalID)
	if err != nil {
		return 0, 0, err
	}
	if f.AllowAgain != nil {
		if err := f.AllowAgain(ctx, placement.Provider, placement.ExternalID); err != nil {
			return 0, 0, err
		}
	}
	workID := f.Resolver.byID(ctx, categoryID, ids)
	if workID <= 0 {
		return 0, 0, fmt.Errorf("%w: that movie could not be added", media.ErrInvalidAdd)
	}
	entry, err := f.Unidentified.Get(ctx, mediaObjectID)
	if err != nil {
		return 0, 0, err
	}
	files := []int64{mediaObjectID}
	if wholeFolder {
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
