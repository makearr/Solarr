package movie

import (
	"context"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/works"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// Files is the movie module's media.PlacingFiles, the shared one with the movie a person names by its TMDB or IMDb id.
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
	shared := works.Files{
		Series: f.Series, Unidentified: f.Unidentified, Clock: f.Clock, AllowAgain: f.AllowAgain,
		Resolve: func(ctx context.Context, categoryID int64, placement media.Placement) (int64, error) {
			ids, err := namedIDs(placement.Provider, placement.ExternalID)
			if err != nil {
				return 0, err
			}
			return f.Resolver.byID(ctx, categoryID, ids), nil
		},
	}
	return shared.PlaceFile(ctx, categoryID, mediaObjectID, placement, wholeFolder)
}
