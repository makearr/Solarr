package importlist_test

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/anilist"
	"github.com/makearr/Solarr/internal/metadata/stashbox"
	"github.com/makearr/Solarr/internal/metadata/tmdb"
	"github.com/makearr/Solarr/internal/platform/config"
	"github.com/makearr/Solarr/internal/service/importlist"
)

type seasonCall struct {
	Season     string
	Year, Page int
}

type fakeAniList struct {
	pages   [][]anilist.Media
	listed  []anilist.ListedMedia
	err     error
	seasons []seasonCall
}

func (f *fakeAniList) Season(_ context.Context, season string, year, page int) ([]anilist.Media, bool, error) {
	f.seasons = append(f.seasons, seasonCall{season, year, page})
	if f.err != nil || page > len(f.pages) {
		return nil, false, f.err
	}
	return f.pages[page-1], true, nil
}

func (f *fakeAniList) UserList(context.Context, string) ([]anilist.ListedMedia, error) {
	return f.listed, f.err
}

type fakeTMDB struct {
	pages     []tmdb.ListPage
	err       error
	requested []string
}

func (f *fakeTMDB) page(path string, page int) (tmdb.ListPage, error) {
	f.requested = append(f.requested, path)
	if f.err != nil {
		return tmdb.ListPage{}, f.err
	}
	return f.pages[page-1], nil
}

func (f *fakeTMDB) List(_ context.Context, id string, page int) (tmdb.ListPage, error) {
	return f.page("list/"+id, page)
}
func (f *fakeTMDB) Trending(_ context.Context, kind, window string, page int) (tmdb.ListPage, error) {
	return f.page("trending/"+kind+"/"+window, page)
}
func (f *fakeTMDB) Popular(_ context.Context, kind string, page int) (tmdb.ListPage, error) {
	return f.page("popular/"+kind, page)
}

func fall2026() importlist.Season { return importlist.Season{Name: anilist.SeasonFall, Year: 2026} }

func animeEntry(id, mal int64, english, romaji, format string) anilist.Media {
	var entry anilist.Media
	entry.ID, entry.IDMal, entry.Format, entry.SeasonYear = id, mal, format, 2027
	entry.Title.English, entry.Title.Romaji = english, romaji
	return entry
}

func entries(t *testing.T, catalogue importlist.Catalogue, listType media.ListType, options importlist.Options) []media.ListEntry {
	t.Helper()
	kind, ok := catalogue.Kind(listType)
	if !ok {
		t.Fatalf("no kind %s", listType)
	}
	found, err := kind.Source.Entries(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	return found
}

func TestTheNextSeasonIsReadPageByPageWithinTheBound(t *testing.T) {
	page := []anilist.Media{animeEntry(41, 9, "", "Rei Example", "TV")}
	client := &fakeAniList{pages: [][]anilist.Media{page, page, page, page, page}}
	catalogue := importlist.NewCatalogue(client, &fakeTMDB{}, fall2026)
	found := entries(t, catalogue, media.ListAniListSeason, importlist.Options{"season": "next"})
	want := []seasonCall{{"WINTER", 2027, 1}, {"WINTER", 2027, 2}, {"WINTER", 2027, 3}}
	if !reflect.DeepEqual(client.seasons, want) || len(found) != 3 {
		t.Errorf("asked %+v, found %d; want the following winter, three pages at most", client.seasons, len(found))
	}
	wantEntry := media.ListEntry{Title: "Rei Example", Year: 2027, Form: "TV", AltTitles: []string{"Rei Example"},
		IDs: []media.ProviderID{{Provider: "anilist", ExternalID: "41"}, {Provider: "mal", ExternalID: "9"}}}
	if !reflect.DeepEqual(found[0], wantEntry) {
		t.Errorf("entry = %+v, want %+v", found[0], wantEntry)
	}
}

func TestANamedSeasonTakesItsYear(t *testing.T) {
	client := &fakeAniList{}
	catalogue := importlist.NewCatalogue(client, &fakeTMDB{}, fall2026)
	entries(t, catalogue, media.ListAniListSeason, importlist.Options{"season": "SPRING", "year": "2019"})
	if want := []seasonCall{{"SPRING", 2019, 1}}; !reflect.DeepEqual(client.seasons, want) {
		t.Errorf("asked %+v, want %+v", client.seasons, want)
	}
}

func TestSeasonOfFollowsAniListsQuarters(t *testing.T) {
	cases := map[time.Month]importlist.Season{
		time.January: {Name: "WINTER", Year: 2026}, time.April: {Name: "SPRING", Year: 2026},
		time.September: {Name: "SUMMER", Year: 2026}, time.December: {Name: "FALL", Year: 2026},
	}
	for month, want := range cases {
		if got := importlist.SeasonOf(time.Date(2026, month, 15, 0, 0, 0, 0, time.UTC)); got != want {
			t.Errorf("%s: %+v, want %+v", month, got, want)
		}
	}
}

func TestAUserListKeepsTheChosenStatuses(t *testing.T) {
	client := &fakeAniList{listed: []anilist.ListedMedia{
		{Status: "COMPLETED", Media: animeEntry(1, 0, "Done", "", "TV")},
		{Status: "PLANNING", Media: animeEntry(2, 0, "Planned", "", "TV")},
		{Status: "CURRENT", Media: animeEntry(3, 0, "Watching", "", "TV")},
	}}
	catalogue := importlist.NewCatalogue(client, &fakeTMDB{}, fall2026)
	titles := func(found []media.ListEntry) []string {
		var out []string
		for _, entry := range found {
			out = append(out, entry.Title)
		}
		return out
	}
	if got := titles(entries(t, catalogue, media.ListAniListUser, importlist.Options{"username": "x"})); !reflect.DeepEqual(got, []string{"Planned", "Watching"}) {
		t.Errorf("default statuses kept %q", got)
	}
	if got := titles(entries(t, catalogue, media.ListAniListUser, importlist.Options{"username": "x", "statuses": "COMPLETED"})); !reflect.DeepEqual(got, []string{"Done"}) {
		t.Errorf("completed kept %q", got)
	}
	client.err = anilist.ErrUnknownUser
	kind, _ := catalogue.Kind(media.ListAniListUser)
	if _, err := kind.Source.Entries(t.Context(), importlist.Options{"username": "x"}); !errors.Is(err, importlist.ErrListNotFound) {
		t.Errorf("unknown user: err = %v", err)
	}
}

func TestATMDBListIsReadToItsLastPageWithItsFilmsMarked(t *testing.T) {
	client := &fakeTMDB{pages: []tmdb.ListPage{
		{TotalPages: 2, Items: []tmdb.ListItem{{ID: 1399, MediaType: "tv", Name: "Example Show", FirstAirDate: "2011-04-17", PosterPath: "/p.jpg"}}},
		{TotalPages: 2, Items: []tmdb.ListItem{{ID: 550, MediaType: "movie", Title: "Example Film", ReleaseDate: "1999-10-15"}}},
	}}
	catalogue := importlist.NewCatalogue(&fakeAniList{}, client, fall2026)
	found := entries(t, catalogue, media.ListTMDBList, importlist.Options{"list": "https://www.themoviedb.org/list/8-my-shows"})
	want := []media.ListEntry{
		{Title: "Example Show", Year: 2011, Form: "tv", IDs: []media.ProviderID{{Provider: "tmdb", ExternalID: "1399"}}, PosterURL: tmdb.ImageURL("/p.jpg")},
		{Title: "Example Film", Year: 1999, Form: "movie", IDs: []media.ProviderID{{Provider: "tmdb", ExternalID: "550"}}},
	}
	if !reflect.DeepEqual(found, want) || !reflect.DeepEqual(client.requested, []string{"list/8", "list/8"}) {
		t.Errorf("found %+v, requested %q", found, client.requested)
	}
}

func TestTMDBChartsAskForShowsAndNameAMissingKey(t *testing.T) {
	client := &fakeTMDB{pages: []tmdb.ListPage{{TotalPages: 1}}}
	catalogue := importlist.NewCatalogue(&fakeAniList{}, client, fall2026)
	entries(t, catalogue, media.ListTMDBTrending, nil)
	entries(t, catalogue, media.ListTMDBPopular, nil)
	if want := []string{"trending/tv/week", "popular/tv"}; !reflect.DeepEqual(client.requested, want) {
		t.Errorf("requested %q, want %q", client.requested, want)
	}
	client.err = tmdb.ErrNoKey
	kind, _ := catalogue.Kind(media.ListTMDBPopular)
	if _, err := kind.Source.Entries(t.Context(), nil); !errors.Is(err, importlist.ErrNeedsKey) {
		t.Errorf("err = %v, want ErrNeedsKey", err)
	}
}

func TestTMDBListIDReadsTheNumberNeverAnotherSite(t *testing.T) {
	cases := map[string]string{
		"8": "8", "\t8\n": "8", "https://www.themoviedb.org/list/8": "8", "https://www.themoviedb.org/list/8-shows?page=2": "8",
		"http://themoviedb.org/list/12/": "12", "https://evil.example/list/8": "", "https://www.themoviedb.org.evil/list/8": "",
		"-3": "", "list": "",
	}
	for value, want := range cases {
		got, ok := importlist.TMDBListID(value)
		if got != want || ok != (want != "") {
			t.Errorf("%q: %q, %v; want %q", value, got, ok, want)
		}
	}
}

func TestValidateChecksTheTypeAndItsSettings(t *testing.T) {
	catalogue := importlist.NewCatalogue(&fakeAniList{}, &fakeTMDB{}, fall2026)
	tvTypes := []media.ListType{media.ListAniListUser, media.ListTMDBList}
	cases := []struct {
		name     string
		listType media.ListType
		options  importlist.Options
		want     config.Problems
	}{
		{"fine", media.ListAniListUser, importlist.Options{"username": "x", "statuses": "CURRENT,COMPLETED"}, nil},
		{"not taken by the library", media.ListTMDBPopular, nil,
			config.Problems{{Path: "type", Value: "tmdb_popular_tv", Expectation: "a list type this library takes"}}},
		{"missing and unknown settings", media.ListAniListUser, importlist.Options{"statuses": "WATCHED", "colour": "red"}, config.Problems{
			{Path: "options.colour", Expectation: "a setting of AniList user list"},
			{Path: "options.statuses", Value: "WATCHED", Expectation: "one of the choices offered"},
			{Path: "options.username", Expectation: "not empty"},
		}},
		{"a link to another site", media.ListTMDBList, importlist.Options{"list": "https://evil.example/list/8"}, config.Problems{
			{Path: "options.list", Value: "https://evil.example/list/8", Expectation: "a TMDB list number or its themoviedb.org link"},
		}},
	}
	for _, c := range cases {
		if got := catalogue.Validate(tvTypes, c.listType, c.options); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: problems = %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestOptionsEncodeAlikeForEqualSettings(t *testing.T) {
	a := importlist.EncodeOptions(importlist.Options{"username": " x ", "statuses": "", "season": "next"})
	b := importlist.EncodeOptions(importlist.Options{"season": "next", "username": "x"})
	if a != b || a != `{"season":"next","username":"x"}` {
		t.Errorf("encoded %q and %q", a, b)
	}
	decoded, err := importlist.DecodeOptions(a)
	if err != nil || !reflect.DeepEqual(decoded, importlist.Options{"season": "next", "username": "x"}) {
		t.Errorf("decoded %+v, err = %v", decoded, err)
	}
	if empty, err := importlist.DecodeOptions(""); err != nil || len(empty) != 0 {
		t.Errorf("empty decoded %+v, err = %v", empty, err)
	}
}

type fakeStash struct {
	scenes     []stashbox.Scene
	configured bool
	asked      []string
}

func (f *fakeStash) Configured() bool { return f.configured }

func (f *fakeStash) FavouriteScenes(_ context.Context, favourite string, page, perPage int) ([]stashbox.Scene, error) {
	f.asked = append(f.asked, favourite+":"+strconv.Itoa(page))
	start := (page - 1) * perPage
	if start >= len(f.scenes) {
		return nil, nil
	}
	return f.scenes[start:min(start+perPage, len(f.scenes))], nil
}

func TestStashFavouritesReadsTheNewestScenesOfTheFavouriteKindAndStopsAtTheCount(t *testing.T) {
	scenes := make([]stashbox.Scene, 120)
	for i := range scenes {
		scenes[i] = stashbox.Scene{ID: "s" + strconv.Itoa(i), Title: "Scene " + strconv.Itoa(i), ReleaseDate: "2026-01-02"}
	}
	server := &fakeStash{scenes: scenes, configured: true}
	catalogue := importlist.NewCatalogue(&fakeAniList{}, &fakeTMDB{}, fall2026).WithStash(map[string]importlist.StashServer{"stashdb": server})
	kind, _ := catalogue.Kind(media.ListStashFavourites)
	entries, err := kind.Source.Entries(t.Context(), importlist.Options{"favourites": "STUDIO", "newest": "100"})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 100 || entries[0].Key() != (media.ProviderID{Provider: "stashdb", ExternalID: "s0"}) || entries[0].Year != 2026 || entries[0].Form != "scene" {
		t.Errorf("entries = %d, first = %+v", len(entries), entries[0])
	}
	if want := []string{"STUDIO:1", "STUDIO:2"}; !reflect.DeepEqual(server.asked, want) {
		t.Errorf("asked %v, want %v", server.asked, want)
	}
}

func TestStashFavouritesWithoutAKeyIsAnErrorNotAnEmptyList(t *testing.T) {
	catalogue := importlist.NewCatalogue(&fakeAniList{}, &fakeTMDB{}, fall2026).WithStash(map[string]importlist.StashServer{"stashdb": &fakeStash{}})
	kind, _ := catalogue.Kind(media.ListStashFavourites)
	if _, err := kind.Source.Entries(t.Context(), importlist.Options{}); !errors.Is(err, importlist.ErrNeedsStashKey) {
		t.Errorf("err = %v, want ErrNeedsStashKey", err)
	}
}
