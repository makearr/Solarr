package library_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

func localArtist(t *testing.T, people library.People, name string) int64 {
	t.Helper()
	id, err := people.ResolvePerson(t.Context(), firstSeen, library.PersonLink{
		Provider: "local:artist", ExternalID: name, Person: library.PersonIdentity{Name: name},
	})
	if err != nil {
		t.Fatalf("ResolvePerson(%q): %v", name, err)
	}
	return id
}

func TestPlanArtistMerges_LocalArtistsJoinTheIdentifiedOneByNameOrAlias(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)
	identified := resolvePersonNamed(t, people, "artist-1", "Vutu Doci")
	if err := people.BackfillDetails(t.Context(), identified, library.PersonIdentity{Name: "Vutu Doci", Aliases: []string{"Vudu Doci"}}); err != nil {
		t.Fatalf("BackfillDetails: %v", err)
	}
	byAlias := localArtist(t, people, "vudu-doci")
	byName := localArtist(t, people, "VUTU DOCI")
	other := localArtist(t, people, "Katupoken")
	for index, personID := range []int64{identified, byAlias, byName, other} {
		work := createWork(t, opened, "music", "Album "+string(rune('A'+index)))
		createDesiredInCategory(t, opened, work.ID, 1)
		if err := people.AddCredit(t.Context(), work.ID, personID, "artist", 0); err != nil {
			t.Fatalf("AddCredit: %v", err)
		}
	}

	merges, err := people.PlanArtistMerges(t.Context(), 1)
	if err != nil {
		t.Fatalf("PlanArtistMerges: %v", err)
	}
	if len(merges) != 2 || merges[0].LoserID != byAlias || merges[1].LoserID != byName ||
		merges[0].WinnerID != identified || merges[1].WinnerID != identified {
		t.Fatalf("merges = %+v, want the alias and the name match folded into %d, Katupoken left", merges, identified)
	}
	elsewhere, err := people.PlanArtistMerges(t.Context(), 2)
	if err != nil || len(elsewhere) != 0 {
		t.Fatalf("another library planned %+v (err %v), want nothing", elsewhere, err)
	}

	if err := people.MergeArtists(t.Context(), merges); err != nil {
		t.Fatalf("MergeArtists: %v", err)
	}
	queries := librarydb.New(opened.Read())
	if _, err := queries.GetPerson(t.Context(), byAlias); err == nil {
		t.Errorf("loser %d still exists", byAlias)
	}
	again, err := people.ResolveFolderArtist(t.Context(), firstSeen, "local:artist", "vudu-doci")
	if err != nil || again != identified {
		t.Errorf("the old folder name resolves to %d (err %v), want the winner %d", again, err, identified)
	}
	if left, _ := people.PlanArtistMerges(t.Context(), 1); len(left) != 0 {
		t.Errorf("a second plan still holds %+v", left)
	}
}

func TestPlanArtistMerges_LocalNamesAlikeMergeIntoTheOneWithMostAlbums(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)
	few := localArtist(t, people, "AD XK")
	many := localArtist(t, people, "AD/XK")
	for _, plan := range []struct {
		person int64
		albums int
	}{{few, 1}, {many, 2}} {
		for range plan.albums {
			work := createWork(t, opened, "music", "Album")
			createDesiredInCategory(t, opened, work.ID, 1)
			if err := people.AddCredit(t.Context(), work.ID, plan.person, "artist", 0); err != nil {
				t.Fatalf("AddCredit: %v", err)
			}
		}
	}

	merges, err := people.PlanArtistMerges(t.Context(), 1)
	if err != nil {
		t.Fatalf("PlanArtistMerges: %v", err)
	}
	if len(merges) != 1 || merges[0].LoserID != few || merges[0].WinnerID != many {
		t.Fatalf("merges = %+v, want %d folded into %d", merges, few, many)
	}
}

func TestPlanArtistMerges_LeavesAnArtistAlsoInAnotherLibrary(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)
	identified := resolvePersonNamed(t, people, "artist-1", "Vutu Doci")
	local := localArtist(t, people, "Vutu Doci")
	for _, credit := range []struct{ person, category int64 }{{identified, 1}, {local, 1}, {local, 2}} {
		work := createWork(t, opened, "music", "Album")
		createDesiredInCategory(t, opened, work.ID, credit.category)
		if err := people.AddCredit(t.Context(), work.ID, credit.person, "artist", 0); err != nil {
			t.Fatalf("AddCredit: %v", err)
		}
	}
	merges, err := people.PlanArtistMerges(t.Context(), 1)
	if err != nil || len(merges) != 0 {
		t.Fatalf("merges = %+v (err %v), want none: the local artist has work in library 2", merges, err)
	}
}

func TestLinkWikidata_TheSameQIDMergesTheTwoAndTheMusicArtistSurvives(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)
	actor, err := people.ResolvePerson(t.Context(), firstSeen, library.PersonLink{
		Provider: "tmdb", ExternalID: "1", Person: library.PersonIdentity{Name: "Vutu Doci"},
	})
	if err != nil {
		t.Fatalf("ResolvePerson actor: %v", err)
	}
	artist := resolvePersonNamed(t, people, "artist-1", "Vutu Doci")
	if _, err := people.LinkWikidata(t.Context(), artist, "q42"); err != nil {
		t.Fatalf("LinkWikidata artist: %v", err)
	}
	if survivor, err := people.LinkWikidata(t.Context(), artist, "Q42"); err != nil || survivor != artist {
		t.Fatalf("relinking own QID = %d, %v; want %d", survivor, err, artist)
	}
	survivor, err := people.LinkWikidata(t.Context(), actor, "Q42")
	if err != nil || survivor != artist {
		t.Fatalf("LinkWikidata actor = %d, %v; want survivor %d", survivor, err, artist)
	}
	if _, err := librarydb.New(opened.Read()).GetPerson(t.Context(), actor); err == nil {
		t.Errorf("merged person %d still exists", actor)
	}
	if id, found, err := people.FindByExternalID(t.Context(), "wikidata", "Q42"); err != nil || !found || id != artist {
		t.Errorf("wikidata Q42 -> %d, %v, %v; want %d", id, found, err, artist)
	}
}

// "'Weird Al' Yankovic" (TMDB) and "Weird Al Yankovic" (MusicBrainz) differ only in punctuation: the bridge asks about
// the pair, and a different name stays out.
func TestBridgeCandidates_NamesThatDifferOnlyInPunctuationMeet(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)
	resolvePersonNamed(t, people, "artist-1", "Weird Al Yankovic")
	actor, err := people.ResolvePerson(t.Context(), firstSeen, library.PersonLink{
		Provider: "tmdb", ExternalID: "7", Person: library.PersonIdentity{Name: "'Weird Al' Yankovic"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := people.ResolvePerson(t.Context(), firstSeen, library.PersonLink{
		Provider: "tmdb", ExternalID: "8", Person: library.PersonIdentity{Name: "Someone Else"},
	}); err != nil {
		t.Fatal(err)
	}
	candidates, err := people.BridgeCandidates(t.Context(), 10)
	if err != nil || len(candidates) != 1 || candidates[0].PersonID != actor || candidates[0].TMDBID != "7" {
		t.Fatalf("candidates = %+v, err = %v, want only the actor", candidates, err)
	}
}
