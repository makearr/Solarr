package app

import (
	"context"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media/adult"
	"github.com/makearr/Solarr/internal/media/book"
	"github.com/makearr/Solarr/internal/media/movie"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// addPlacement is how the add dialog's root folder choice becomes each type's placement affinity.
func (p *process) addPlacement() *adapters.AddPlacement {
	keys := map[string]func(title string, year int64) string{
		tv.MediaType:    func(title string, _ int64) string { return tv.PlacementKey(title) },
		movie.MediaType: movie.PlacementKey,
		adult.MediaType: func(title string, _ int64) string { return adult.PlacementKey(title) },
	}
	for _, format := range book.Formats {
		keys[format.MediaType] = func(title string, _ int64) string { return book.PlacementKey(format, title) }
	}
	clocks := clock.NewSystem()
	reader := library.NewSeriesReader(p.library)
	titled := func(ctx context.Context, mediaType string, categoryID, workID int64) (string, int64, error) {
		detail, err := reader.GetOf(ctx, mediaType, categoryID, workID, clocks.Now())
		return detail.Summary.Title, detail.Summary.Year, err
	}
	return &adapters.AddPlacement{Titled: titled, Affinity: p.cache, Clock: clocks, Keys: keys}
}
