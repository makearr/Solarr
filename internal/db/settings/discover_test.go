package settings_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
)

func TestDiscoverIsOffWithDefaultsUntilSaved(t *testing.T) {
	cache, _ := newCache(t)
	movies, _ := cache.CreateCategory(t.Context(), "movie", "Movies", 10)
	if got := cache.Snapshot().DiscoverGlobal(); !reflect.DeepEqual(got, settings.DiscoverGlobal{}) {
		t.Errorf("global = %+v", got)
	}
	got, err := cache.LibraryDiscover(t.Context(), movies.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := settings.LibraryDiscover{
		CategoryID: movies.ID, TotalAmount: 60, ClassicAgeYears: 25, MinRating: 7, MinVotes: 1000, AutoAddPerDay: 5,
		Types: []settings.DiscoverTypeSetting{
			{Type: "highest_rated", Enabled: true, Amount: 20}, {Type: "trending", Enabled: true, Amount: 20}, {Type: "upcoming", Enabled: true, Amount: 20},
			{Type: "classics", Enabled: true, Amount: 20}, {Type: "collections", Enabled: true, Amount: 20},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("library = %+v\nwant %+v", got, want)
	}
}

func TestDiscoverRoundTripsAndFollowsTheCategoryOnDelete(t *testing.T) {
	cache, cl := newCache(t)
	series, _ := cache.CreateCategory(t.Context(), "tv", "Series", 10)
	global := settings.DiscoverGlobal{Enabled: true, ExcludeGenres: []string{"Reality", "Reality", " "}, ExcludeNetworks: []string{"Example Network"}}
	if err := cache.SetDiscoverGlobal(t.Context(), cl, global); err != nil {
		t.Fatal(err)
	}
	if got, want := cache.Snapshot().DiscoverGlobal(), (settings.DiscoverGlobal{Enabled: true, ExcludeGenres: []string{"Reality"}, ExcludeNetworks: []string{"Example Network"}}); !reflect.DeepEqual(got, want) {
		t.Errorf("global = %+v, want %+v", got, want)
	}
	want := settings.LibraryDiscover{
		CategoryID: series.ID, Enabled: true, TotalAmount: 40, ClassicAgeYears: 30, MinRating: 7.5, MinVotes: 500, AutoAddPerDay: 2, AutoAddSearch: true,
		OriginalLanguages: []string{"en", "ja"}, LanguagesSet: true, ExcludeGenres: []string{"Talk"}, ExcludeNetworks: []string{"Example Cable"}, DisabledKinds: []string{"performer"},
		Types: []settings.DiscoverTypeSetting{
			{Type: "highest_rated", Enabled: true, Amount: 30, AutoAdd: true}, {Type: "trending", Enabled: false, Amount: 20}, {Type: "upcoming", Enabled: true, Amount: 20},
			{Type: "classics", Enabled: true, Amount: 20}, {Type: "collections", Enabled: true, Amount: 20},
		},
	}
	if err := cache.SetLibraryDiscover(t.Context(), want); err != nil {
		t.Fatal(err)
	}
	got, err := cache.LibraryDiscover(t.Context(), series.ID)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("library = %+v, %v\nwant %+v", got, err, want)
	}
	if err := cache.DeleteCategory(t.Context(), series.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.LibraryDiscover(t.Context(), series.ID); !errors.Is(err, settings.ErrNotFound) {
		t.Errorf("after delete err = %v", err)
	}
	rows, err := cache.LibraryDiscovers(t.Context())
	if err != nil || len(rows) != 0 {
		t.Errorf("libraries after delete = %+v, %v", rows, err)
	}
}

func TestDiscoverRefusesValuesOutsideTheirBounds(t *testing.T) {
	cache, _ := newCache(t)
	movies, _ := cache.CreateCategory(t.Context(), "movie", "Movies", 10)
	base, _ := cache.LibraryDiscover(t.Context(), movies.ID)
	bad := []func(*settings.LibraryDiscover){
		func(d *settings.LibraryDiscover) { d.TotalAmount = 0 },
		func(d *settings.LibraryDiscover) { d.ClassicAgeYears = 151 },
		func(d *settings.LibraryDiscover) { d.MinRating = 10.5 },
		func(d *settings.LibraryDiscover) { d.MinVotes = -1 },
		func(d *settings.LibraryDiscover) { d.AutoAddPerDay = 101 },
		func(d *settings.LibraryDiscover) { d.Types[0].Amount = 201 },
		func(d *settings.LibraryDiscover) { d.Types[0].Type = "popular" },
	}
	for index, change := range bad {
		candidate := base
		candidate.Types = append([]settings.DiscoverTypeSetting{}, base.Types...)
		change(&candidate)
		if err := cache.SetLibraryDiscover(t.Context(), candidate); err == nil {
			t.Errorf("case %d accepted: %+v", index, candidate)
		}
	}
	if err := cache.SetLibraryDiscover(t.Context(), settings.LibraryDiscover{CategoryID: 999, TotalAmount: 10, ClassicAgeYears: 1}); !errors.Is(err, settings.ErrNotFound) {
		t.Errorf("unknown library err = %v", err)
	}
}
