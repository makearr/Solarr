package library_test

import (
	"testing"

	"github.com/makearr/MediaNexus/internal/library"
	"github.com/makearr/MediaNexus/internal/store/gen/librarydb"
)

// newWorkForCredits creates a bare work to attach credits/genres to, via the
// same ResolveWorks path a real Identifier chain uses.
func newWorkForCredits(t *testing.T, identity library.Identity, queries *librarydb.Queries, externalID string) int64 {
	t.Helper()
	_, err := identity.ResolveWorks(t.Context(), firstSeen, []library.WorkLink{{
		Provider: "musicbrainz:release-group", ExternalID: externalID, Confidence: 1.0,
		Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: "KI Pecezinu", SortTitle: "ki pecezinu"},
	}})
	if err != nil {
		t.Fatalf("ResolveWorks: %v", err)
	}
	work, err := queries.GetWorkByExternalID(t.Context(), librarydb.GetWorkByExternalIDParams{
		Provider: "musicbrainz:release-group", ExternalID: externalID,
	})
	if err != nil {
		t.Fatalf("GetWorkByExternalID: %v", err)
	}
	return work.ID
}

func TestResolvePerson_CreatesOnceAndReusesOnASecondCall(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)

	link := library.PersonLink{
		Provider: "musicbrainz:artist", ExternalID: "artist-1",
		Person: library.PersonIdentity{
			Name: "Vutu Doci", SortName: "Bush, Vutu", Kind: "Person",
			Gender: "Female", Country: "GB", Born: "1958-07-30",
		},
	}

	first, err := people.ResolvePerson(t.Context(), firstSeen, link)
	if err != nil {
		t.Fatalf("ResolvePerson: %v", err)
	}
	if first == 0 {
		t.Fatal("ResolvePerson returned id 0")
	}

	second, err := people.ResolvePerson(t.Context(), firstSeen, link)
	if err != nil {
		t.Fatalf("ResolvePerson (second): %v", err)
	}
	if second != first {
		t.Errorf("second ResolvePerson = %d, want the same id %d as the first call", second, first)
	}
}

func TestResolvePerson_StoresBiographyInFieldsJSON(t *testing.T) {
	opened, queries := newLibrary(t)
	people := library.NewPeople(opened)

	personID, err := people.ResolvePerson(t.Context(), firstSeen, library.PersonLink{
		Provider: "musicbrainz:artist", ExternalID: "artist-2",
		Person: library.PersonIdentity{
			Name: "Vutu Doci", SortName: "Bush, Vutu", Biography: "English singer-songwriter.",
		},
	})
	if err != nil {
		t.Fatalf("ResolvePerson: %v", err)
	}

	row, err := queries.GetPerson(t.Context(), personID)
	if err != nil {
		t.Fatalf("GetPerson: %v", err)
	}
	if !row.FieldsJson.Valid || row.FieldsJson.String == "" {
		t.Fatal("fields_json is empty, want the biography recorded")
	}
}

func TestAddCredit_RecordsAWorkLevelCredit(t *testing.T) {
	opened, queries := newLibrary(t)
	identity := library.NewIdentity(opened)
	people := library.NewPeople(opened)
	workID := newWorkForCredits(t, identity, queries, "rg-credit-1")

	personID, err := people.ResolvePerson(t.Context(), firstSeen, library.PersonLink{
		Provider: "musicbrainz:artist", ExternalID: "artist-3",
		Person: library.PersonIdentity{Name: "Katupoken", SortName: "Katupoken", Kind: "Group"},
	})
	if err != nil {
		t.Fatalf("ResolvePerson: %v", err)
	}
	if err := people.AddCredit(t.Context(), workID, personID, "artist", 0); err != nil {
		t.Fatalf("AddCredit: %v", err)
	}

	credits, err := queries.ListCreditsForWork(t.Context(), workID)
	if err != nil {
		t.Fatalf("ListCreditsForWork: %v", err)
	}
	if len(credits) != 1 || credits[0].PersonID != personID || credits[0].Role != "artist" {
		t.Errorf("credits = %+v, want one artist credit for person %d", credits, personID)
	}
}

func TestAssignGenres_CreatesTagsAndAssignsThem(t *testing.T) {
	opened, queries := newLibrary(t)
	identity := library.NewIdentity(opened)
	people := library.NewPeople(opened)
	workID := newWorkForCredits(t, identity, queries, "rg-credit-2")

	err := people.AssignGenres(t.Context(), workID, []library.GenreLink{
		{Name: "Alternative Rock"}, {Name: "Art Rock"},
	})
	if err != nil {
		t.Fatalf("AssignGenres: %v", err)
	}

	assignments, err := queries.ListTagsForSubject(t.Context(), librarydb.ListTagsForSubjectParams{
		SubjectType: "work", SubjectID: workID,
	})
	if err != nil {
		t.Fatalf("ListTagsForSubject: %v", err)
	}
	if len(assignments) != 2 {
		t.Fatalf("assignments = %+v, want 2 genre tags", assignments)
	}
	for _, assignment := range assignments {
		if assignment.Source != "musicbrainz" {
			t.Errorf("Source = %q, want musicbrainz", assignment.Source)
		}
	}
}

// ADR-33: a refresh must only ever remove what it itself assigned. A
// hand-added user tag on the same work must survive AssignGenres clearing
// musicbrainz's own prior assignments.
func TestAssignGenres_NeverRemovesAUserTag(t *testing.T) {
	opened, queries := newLibrary(t)
	identity := library.NewIdentity(opened)
	people := library.NewPeople(opened)
	workID := newWorkForCredits(t, identity, queries, "rg-credit-3")

	if err := people.AssignGenres(t.Context(), workID, []library.GenreLink{{Name: "Art Rock"}}); err != nil {
		t.Fatalf("first AssignGenres: %v", err)
	}
	userTag, err := queries.CreateTag(t.Context(), librarydb.CreateTagParams{
		Name: "Favourites", Slug: "favourites", Source: "user",
	})
	if err != nil {
		t.Fatalf("CreateTag: %v", err)
	}
	if err := queries.AssignTag(t.Context(), librarydb.AssignTagParams{
		TagID: userTag.ID, SubjectType: "work", SubjectID: workID, Source: "user",
	}); err != nil {
		t.Fatalf("AssignTag: %v", err)
	}

	// A refresh that now finds a different genre set.
	if err := people.AssignGenres(t.Context(), workID, []library.GenreLink{{Name: "Electronic"}}); err != nil {
		t.Fatalf("second AssignGenres: %v", err)
	}

	assignments, err := queries.ListTagsForSubject(t.Context(), librarydb.ListTagsForSubjectParams{
		SubjectType: "work", SubjectID: workID,
	})
	if err != nil {
		t.Fatalf("ListTagsForSubject: %v", err)
	}
	var sources []string
	for _, assignment := range assignments {
		sources = append(sources, assignment.Source)
	}
	if len(assignments) != 2 {
		t.Fatalf("assignments = %+v, want the new musicbrainz genre plus the surviving user tag", sources)
	}
}
