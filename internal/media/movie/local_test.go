package movie_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/media/movie"
	"github.com/makearr/Solarr/internal/mediaserver"
)

type fakeLocal struct {
	item mediaserver.Item
	seen []string
}

func (f *fakeLocal) Ask(_ context.Context, _ int64, kind, mediaType string, _ map[string]string) (mediaserver.Item, bool, error) {
	f.seen = append(f.seen, kind+" "+mediaType)
	return f.item, true, nil
}

func (f *fakeLocal) AskByPath(_ context.Context, _ int64, path string) (mediaserver.Item, bool, error) {
	f.seen = append(f.seen, path)
	return f.item, true, nil
}

func TestIDsByPathNamesAFileByTheServersIDsOnlyForAFilm(t *testing.T) {
	service, _ := newService(t, fakeTMDB{}, nil)
	service.Local = &fakeLocal{item: mediaserver.Item{MediaType: "movie", IDs: map[string]string{"tmdb": "603", "imdb": "tt9"}}}
	if got, want := service.IDsByPath(t.Context(), 2, "/films/Example (1999)/Example.mkv"), (movie.IDs{TMDB: 603, IMDb: "tt9"}); got != want {
		t.Errorf("ids = %+v, want %+v", got, want)
	}
	service.Local = &fakeLocal{item: mediaserver.Item{MediaType: "tv", IDs: map[string]string{"tmdb": "603"}}}
	if got := service.IDsByPath(t.Context(), 2, "/films/Example.mkv"); got != (movie.IDs{}) {
		t.Errorf("a series named a film: %+v", got)
	}
}

func TestAMediaServerFillsWhatTheProvidersLackAndNeverReplacesTheFilesID(t *testing.T) {
	service, _ := newService(t, fakeTMDB{configured: true, movie: theExample()}, nil)
	local := &fakeLocal{item: mediaserver.Item{Title: "Home Name", Genres: []string{"Heist"}, AgeRating: "PG", IDs: map[string]string{"tmdb": "1", "imdb": "tt42"}}}
	service.Local = local
	service.PriorityFor = func(int64) []string { return []string{"tmdb", "local:jellyfin"} }
	got, err := service.FetchFor(t.Context(), 3, movie.IDs{TMDB: 603})
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "The Example Film" || got.IDs.TMDB != 603 || got.IDs.IMDb != "tt0000001" {
		t.Errorf("title %q, ids %+v: the provider's title and the file's id stay, the server replaces neither", got.Title, got.IDs)
	}
	if want := []string{"jellyfin movie"}; !reflect.DeepEqual(local.seen, want) {
		t.Errorf("asked = %q, want %q", local.seen, want)
	}
}
