package movie_test

import (
	"context"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/parsemovie"
	"github.com/makearr/Solarr/internal/media/movie"
	"github.com/makearr/Solarr/internal/metadata/tmdb"
)

func TestAnAliasThatIsPartOfAnotherFilmsTitleDoesNotPlaceAFolderInTheLibrary(t *testing.T) {
	example := theExample()
	example.Title, example.ReleaseDate = "Zuuzxopva nen Hife's Saxewtvei", "2022-11-09"
	service, opened := newService(t, fakeTMDB{configured: true, movie: example}, nil)
	added, err := service.Add(t.Context(), movie.AddRequest{IDs: movie.IDs{TMDB: 603}, CategoryID: 5, ProfileID: 1, Monitor: movie.MonitorNone})
	if err != nil {
		t.Fatal(err)
	}
	year := int64(2022)
	if _, err := service.Series.Upsert(t.Context(), addNow, library.SeriesWrite{
		Work: library.WorkIdentity{MediaType: movie.MediaType, CanonicalTitle: example.Title, SortTitle: "zuuzxopva nen hife's saxewtvei"}, Year: &year,
		IDs: []library.ExternalIDWrite{{Provider: movie.ProviderTMDB, ExternalID: "603"}}, Aliases: []string{"Saxewtvei", "Pinocho"}, AliasSource: "test",
	}); err != nil {
		t.Fatal(err)
	}
	resolver := movie.Resolver{Service: service, Reader: library.NewSeriesReader(opened), DefaultProfileID: 1, Categories: categories{}}
	if id, _ := resolver.Resolve(t.Context(), 5, parsemovie.Info{Title: "Saxewtvei", Year: 2022}, movie.IDs{}); id == added.WorkID {
		t.Errorf("the folder \"Saxewtvei (2022)\" was placed on work %d through its short alias", id)
	}
	if id, _ := resolver.Resolve(t.Context(), 5, parsemovie.Info{Title: "Pinocho", Year: 2022}, movie.IDs{}); id != added.WorkID {
		t.Errorf("a translated alias places the folder: %d, want %d", id, added.WorkID)
	}
}

func TestAYearOffByOneIsPlacedAtTheDefaultThresholdAndHeldAboveIt(t *testing.T) {
	service, opened := newService(t, fakeTMDB{configured: true, movie: theExample()}, nil)
	info := parsemovie.Info{Title: "The Example Film", Year: 1998}
	// Held first: once added, the film is found in the library at any threshold.
	for _, c := range []struct {
		name      string
		threshold func() float64
		placed    bool
	}{
		{"0.95", func() float64 { return 0.95 }, false},
		{"0.90", func() float64 { return 0.9 }, true},
	} {
		resolver := movie.Resolver{Service: service, Reader: library.NewSeriesReader(opened), DefaultProfileID: 1, Categories: categories{}, Threshold: c.threshold}
		if id, _ := resolver.Resolve(t.Context(), 5, info, movie.IDs{}); (id > 0) != c.placed {
			t.Errorf("threshold %s: work %d, placed %v; want placed %v", c.name, id, id > 0, c.placed)
		}
	}
}

func TestAFolderNameLongerThanTheProvidersTitleIsFoundByItsTrailingWords(t *testing.T) {
	example := theExample()
	example.Title, example.ReleaseDate = "Night of the Living Pharmacists", "2014-08-01"
	service, opened := newService(t, shortQueriesOnly{fakeTMDB{configured: true, movie: example}}, nil)
	resolver := movie.Resolver{Service: service, Reader: library.NewSeriesReader(opened), DefaultProfileID: 1, Categories: categories{}}
	if id, _ := resolver.Resolve(t.Context(), 5, parsemovie.Info{Title: "Phineas and Ferb Night of the Living Pharmacists", Year: 2014}, movie.IDs{}); id <= 0 {
		t.Errorf("work = %d, want the film placed through the folder name's trailing words", id)
	}
}

// shortQueriesOnly finds nothing for a long query, as a provider does for a title it holds in fewer words.
type shortQueriesOnly struct{ fakeTMDB }

func (f shortQueriesOnly) SearchMovies(ctx context.Context, query string, year, page int) ([]tmdb.MovieHit, error) {
	if len(strings.Fields(query)) > 6 {
		return nil, nil
	}
	return f.fakeTMDB.SearchMovies(ctx, query, year, page)
}
