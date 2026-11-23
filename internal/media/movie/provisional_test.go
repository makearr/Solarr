package movie_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media/movie"
	"github.com/makearr/Solarr/internal/mediaserver"
)

func TestAFilmOnlyTheMediaServerKnowsIsMarkedProvisional(t *testing.T) {
	cases := []struct {
		name string
		tmdb fakeTMDB
		want library.MetadataMark
	}{
		{"TMDB answered", fakeTMDB{configured: true, movie: theExample()}, library.MarkSettled},
		{"only the server answered", fakeTMDB{}, library.MarkProvisional},
	}
	for _, c := range cases {
		service, _ := newService(t, c.tmdb, nil)
		service.Local = &fakeLocal{item: mediaserver.Item{Title: "Home Name", Genres: []string{"Heist"}}}
		service.PriorityFor = func(int64) []string { return []string{"tmdb", "local:jellyfin"} }
		got, err := service.FetchFor(t.Context(), 3, movie.IDs{TMDB: 603})
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if mark := movie.ToWrite(got, movie.WriteOptions{}).Mark; mark != c.want {
			t.Errorf("%s: mark = %v, want %v (sources %v)", c.name, mark, c.want, got.Sources)
		}
	}
}
