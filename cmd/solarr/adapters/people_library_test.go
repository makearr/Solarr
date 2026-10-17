package adapters_test

import (
	"testing"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
)

func TestAPersonsKnownForGoesToTheLibraryOfItsKind(t *testing.T) {
	cache := newSweepCache(t)
	series, err := cache.CreateCategory(t.Context(), "tv", "Series", 0)
	if err != nil {
		t.Fatal(err)
	}
	films, err := cache.CreateCategory(t.Context(), "movie", "Movies", 0)
	if err != nil {
		t.Fatal(err)
	}
	people := adapters.People{Categories: adapters.Categories{Cache: cache}}
	for _, one := range []struct {
		kind string
		page int64
		want int64
	}{{"movie", series.ID, films.ID}, {"tv", series.ID, series.ID}, {"tv", films.ID, series.ID}, {"album", series.ID, 0}} {
		if got := people.LibraryFor(one.kind, one.page); got != one.want {
			t.Errorf("LibraryFor(%q, page %d) = %d, want %d", one.kind, one.page, got, one.want)
		}
	}
	if got := (adapters.People{}).LibraryFor("movie", 1); got != 0 {
		t.Errorf("an adapter with no categories answers %d, want none and no panic", got)
	}
}
