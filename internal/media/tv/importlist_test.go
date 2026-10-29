package tv_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/metadata/skyhook"
)

// termSkyhook answers each search term with its own hits and records the terms asked.
type termSkyhook struct {
	hits  map[string][]skyhook.Series
	asked *[]string
	err   error
}

func (f termSkyhook) Series(context.Context, int64) (skyhook.Series, error) {
	return skyhook.Series{}, nil
}
func (f termSkyhook) Search(_ context.Context, term string) ([]skyhook.Series, error) {
	*f.asked = append(*f.asked, term)
	return f.hits[term], f.err
}

func TestListEntriesResolveToTheIDTheSeriesIsAddedBy(t *testing.T) {
	var asked []string
	lists := tv.Lists{Service: tv.Service{Skyhook: termSkyhook{asked: &asked, hits: map[string][]skyhook.Series{
		// The first title's hits list another season's AniList ids only; the romaji title finds the series.
		"Example Ray":     {{TVDBID: 501, AniListIDs: []int64{90}}},
		"Rei Example":     {{TVDBID: 502, AniListIDs: []int64{40, 41}}, {TVDBID: 503, AniListIDs: []int64{41}}},
		"Nothing Matches": {{TVDBID: 504, AniListIDs: []int64{7}}},
	}}}}
	cases := []struct {
		name  string
		entry media.ListEntry
		want  media.ListMatch
	}{
		{"tmdb show", media.ListEntry{Form: "tv", IDs: []media.ProviderID{{Provider: "tmdb", ExternalID: "1399"}}},
			media.ListMatch{ProviderID: media.ProviderID{Provider: "tmdb", ExternalID: "1399"}, Found: true}},
		{"tmdb film", media.ListEntry{Form: "movie", IDs: []media.ProviderID{{Provider: "tmdb", ExternalID: "550"}}},
			media.ListMatch{Foreign: true}},
		{"anilist series", media.ListEntry{Title: "Example Ray", AltTitles: []string{"Example Ray", "Rei Example"}, Form: "TV",
			IDs: []media.ProviderID{{Provider: "anilist", ExternalID: "41"}, {Provider: "mal", ExternalID: "9"}}},
			media.ListMatch{ProviderID: media.ProviderID{Provider: "tvdb", ExternalID: "502"}, Found: true}},
		{"anilist film", media.ListEntry{Title: "Eiga", Form: "MOVIE", IDs: []media.ProviderID{{Provider: "anilist", ExternalID: "12"}}},
			media.ListMatch{Foreign: true}},
		{"anilist unknown to skyhook", media.ListEntry{Title: "Nothing Matches", Form: "ONA", IDs: []media.ProviderID{{Provider: "anilist", ExternalID: "13"}}},
			media.ListMatch{}},
		{"no id the type knows", media.ListEntry{Title: "X", IDs: []media.ProviderID{{Provider: "trakt", ExternalID: "1"}}}, media.ListMatch{}},
	}
	for _, c := range cases {
		got, err := lists.ResolveListEntry(t.Context(), c.entry)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: match = %+v, err = %v; want %+v", c.name, got, err, c.want)
		}
	}
	if want := []string{"Example Ray", "Rei Example", "Nothing Matches"}; !reflect.DeepEqual(asked, want) {
		t.Errorf("searched %q, want %q: each distinct name once, films never", asked, want)
	}
}

func TestAnAniListEntryWithoutASeriesSourceIsAnOutage(t *testing.T) {
	entry := media.ListEntry{Title: "A", Form: "TV", IDs: []media.ProviderID{{Provider: "anilist", ExternalID: "1"}}}
	if _, err := (tv.Lists{}).ResolveListEntry(t.Context(), entry); !errors.Is(err, media.ErrSourceUnavailable) {
		t.Errorf("err = %v", err)
	}
	var asked []string
	failing := tv.Lists{Service: tv.Service{Skyhook: termSkyhook{asked: &asked, err: errors.New("down")}}}
	if _, err := failing.ResolveListEntry(t.Context(), entry); !errors.Is(err, media.ErrSourceUnavailable) {
		t.Errorf("failing source: err = %v", err)
	}
}
