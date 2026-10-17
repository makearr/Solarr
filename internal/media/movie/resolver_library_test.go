package movie_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/parsemovie"
	"github.com/makearr/Solarr/internal/media/movie"
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
