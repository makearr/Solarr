package movie_test

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/movie"
)

func TestResolveListEntry_AddsFilmsByTMDBIDAndLeavesShowsAlone(t *testing.T) {
	tmdb := media.ProviderID{Provider: "tmdb", ExternalID: "603"}
	added := media.ProviderID{Provider: movie.ProviderTMDB, ExternalID: "603"}
	for name, test := range map[string]struct {
		entry media.ListEntry
		want  media.ListMatch
	}{
		"a film":                 {media.ListEntry{Form: "movie", IDs: []media.ProviderID{{Provider: "mdblist", ExternalID: "a"}, tmdb}}, media.ListMatch{ProviderID: added, Found: true}},
		"a show":                 {media.ListEntry{Form: "tv", IDs: []media.ProviderID{tmdb}}, media.ListMatch{Foreign: true}},
		"a film with no TMDB id": {media.ListEntry{Form: "movie", IDs: []media.ProviderID{{Provider: "mdblist", ExternalID: "a"}}}, media.ListMatch{}},
	} {
		got, err := movie.Module{}.ResolveListEntry(t.Context(), test.entry)
		if err != nil || !reflect.DeepEqual(got, test.want) {
			t.Errorf("%s: got %+v, %v; want %+v", name, got, err, test.want)
		}
	}
}
