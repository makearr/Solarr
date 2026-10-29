package adapters_test

import (
	"maps"
	"testing"
	"time"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
)

// An album is looked up for the first music library that wants it, else the one holding its files; an artist for the
// first music library holding their works; anything else for none.
func TestEntityLibraries_NameTheLibraryAnAlbumOrArtistIsLookedUpFor(t *testing.T) {
	cache := newSweepCache(t)
	opened := newSuggestLibrary(t)
	categories := map[string]int64{}
	for index, name := range []string{"Series", "Music", "Classical"} {
		mediaType := map[string]string{"Series": "tv", "Music": "music", "Classical": "music"}[name]
		category, err := cache.CreateCategory(t.Context(), mediaType, name, int64(index))
		if err != nil {
			t.Fatal(err)
		}
		categories[name] = category.ID
	}
	root, err := cache.CreateRootFolder(t.Context(), "/music/classical", "Classical")
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.AttachRootFolder(t.Context(), settings.CategoryRoot{CategoryID: categories["Classical"], RootFolderID: root.ID, AcceptsNew: true}); err != nil {
		t.Fatal(err)
	}

	identity, desired, people := library.NewIdentity(opened), library.NewDesired(opened), library.NewPeople(opened)
	now := time.Unix(1_700_000_000, 0)
	resolve := func(mbid string, rootFolderID int64) int64 {
		t.Helper()
		ids, err := identity.ResolveWorks(t.Context(), now, []library.WorkLink{{
			Provider: "musicbrainz:release-group", ExternalID: mbid, Confidence: 1,
			Work:         library.WorkIdentity{MediaType: "music", CanonicalTitle: mbid, SortTitle: mbid},
			RootFolderID: rootFolderID,
		}})
		if err != nil {
			t.Fatal(err)
		}
		return ids[0]
	}
	onDisk := resolve("on-disk", root.ID)
	wantedToo := resolve("wanted-too", root.ID)
	inSeries := resolve("in-series", 0)
	for workID, categoryID := range map[int64]int64{wantedToo: categories["Music"], inSeries: categories["Series"]} {
		if _, err := desired.EnsureAlbumItem(t.Context(), library.AlbumRequest{WorkID: workID, CategoryID: categoryID, ProfileID: 1}); err != nil {
			t.Fatal(err)
		}
	}
	artist, err := people.ResolvePerson(t.Context(), now, library.PersonLink{
		Provider: "musicbrainz:artist", ExternalID: "artist", Person: library.PersonIdentity{Name: "Katupoken"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := people.AddLeadCredit(t.Context(), wantedToo, artist, "artist"); err != nil {
		t.Fatal(err)
	}
	unheard, err := people.ResolvePerson(t.Context(), now, library.PersonLink{
		Provider: "musicbrainz:artist", ExternalID: "unheard", Person: library.PersonIdentity{Name: "Dipa Cogaf"},
	})
	if err != nil {
		t.Fatal(err)
	}

	libraries := adapters.EntityLibraries{MediaType: "music", Works: library.NewWorks(opened), People: people, Cache: cache}
	got := map[string]int64{
		"files only":        libraries.OfWork(t.Context(), onDisk),
		"wanted and files":  libraries.OfWork(t.Context(), wantedToo),
		"wanted by TV":      libraries.OfWork(t.Context(), inSeries),
		"no such work":      libraries.OfWork(t.Context(), 999_999),
		"credited artist":   libraries.OfPerson(t.Context(), artist),
		"uncredited artist": libraries.OfPerson(t.Context(), unheard),
		"no such artist":    libraries.OfPerson(t.Context(), 999_999),
	}
	want := map[string]int64{
		"files only":        categories["Classical"],
		"wanted and files":  categories["Music"],
		"wanted by TV":      0,
		"no such work":      0,
		"credited artist":   categories["Music"],
		"uncredited artist": 0,
		"no such artist":    0,
	}
	if !maps.Equal(got, want) {
		t.Errorf("libraries = %v, want %v", got, want)
	}
}
