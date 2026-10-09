package library_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
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
			Name: "Vutu Doci", Kind: "Person",
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
			Name: "Vutu Doci", Biography: "English singer-songwriter.",
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
		Person: library.PersonIdentity{Name: "Katupoken", Kind: "Group"},
	})
	if err != nil {
		t.Fatalf("ResolvePerson: %v", err)
	}
	if err := people.AddLeadCredit(t.Context(), workID, personID, "artist"); err != nil {
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

// The Tracks navigation level's own denormalized sort key (ADR-54,
// 0021_work_primary_artist.sql): the primary (ord=0, role="artist") credit
// must refresh work.primary_artist_id/primary_artist_sort_name, since
// AddCredit is its only writer.
func TestAddCredit_OrdZeroArtistSetsWorkPrimaryArtist(t *testing.T) {
	opened, queries := newLibrary(t)
	identity := library.NewIdentity(opened)
	people := library.NewPeople(opened)
	workID := newWorkForCredits(t, identity, queries, "rg-credit-primary")

	personID, err := people.ResolvePerson(t.Context(), firstSeen, library.PersonLink{
		Provider: "musicbrainz:artist", ExternalID: "artist-primary",
		Person: library.PersonIdentity{Name: "Katupoken"},
	})
	if err != nil {
		t.Fatalf("ResolvePerson: %v", err)
	}
	if err := people.AddLeadCredit(t.Context(), workID, personID, "artist"); err != nil {
		t.Fatalf("AddCredit: %v", err)
	}

	work, err := queries.GetWork(t.Context(), workID)
	if err != nil {
		t.Fatalf("GetWork: %v", err)
	}
	if !work.PrimaryArtistID.Valid || work.PrimaryArtistID.Int64 != personID {
		t.Errorf("PrimaryArtistID = %+v, want %d", work.PrimaryArtistID, personID)
	}
	if !work.PrimaryArtistSortName.Valid || work.PrimaryArtistSortName.String != "Katupoken" {
		t.Errorf("PrimaryArtistSortName = %+v, want \"Katupoken\"", work.PrimaryArtistSortName)
	}
}

// A credit that is not the primary artist (a featured performer, ord > 0, or
// any non-"artist" role) must never touch the denormalized column -- a second
// AddCredit call for a featured artist must not overwrite the real primary.
func TestAddCredit_NonPrimaryCreditDoesNotSetWorkPrimaryArtist(t *testing.T) {
	opened, queries := newLibrary(t)
	identity := library.NewIdentity(opened)
	people := library.NewPeople(opened)
	workID := newWorkForCredits(t, identity, queries, "rg-credit-non-primary")

	primary, err := people.ResolvePerson(t.Context(), firstSeen, library.PersonLink{
		Provider: "musicbrainz:artist", ExternalID: "artist-primary-2",
		Person: library.PersonIdentity{Name: "Katupoken"},
	})
	if err != nil {
		t.Fatalf("ResolvePerson: %v", err)
	}
	featured, err := people.ResolvePerson(t.Context(), firstSeen, library.PersonLink{
		Provider: "musicbrainz:artist", ExternalID: "artist-featured",
		Person: library.PersonIdentity{Name: "Someone Else"},
	})
	if err != nil {
		t.Fatalf("ResolvePerson: %v", err)
	}
	if err := people.AddLeadCredit(t.Context(), workID, primary, "artist"); err != nil {
		t.Fatalf("AddCredit (primary): %v", err)
	}
	if err := people.AddCredit(t.Context(), workID, featured, "artist", 1); err != nil {
		t.Fatalf("AddCredit (featured, ord 1): %v", err)
	}
	if err := people.AddCredit(t.Context(), workID, featured, "producer", 0); err != nil {
		t.Fatalf("AddCredit (producer, ord 0): %v", err)
	}

	work, err := queries.GetWork(t.Context(), workID)
	if err != nil {
		t.Fatalf("GetWork: %v", err)
	}
	if !work.PrimaryArtistID.Valid || work.PrimaryArtistID.Int64 != primary {
		t.Errorf("PrimaryArtistID = %+v, want the original primary %d unchanged", work.PrimaryArtistID, primary)
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

// A person first made from a release's credit name takes the artist's own name once looked up (found 2026-09-30).
func TestBackfillDetails_CorrectsACreditNameToTheArtistName(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)
	personID := resolvePersonNamed(t, people, "artist-1", "VUTU")

	if err := people.BackfillDetails(t.Context(), personID, library.PersonIdentity{Name: "Vutu Doci", Kind: "Person"}); err != nil {
		t.Fatalf("BackfillDetails: %v", err)
	}
	person, err := librarydb.New(opened.Read()).GetPerson(t.Context(), personID)
	if err != nil {
		t.Fatalf("GetPerson: %v", err)
	}
	if person.Name != "Vutu Doci" || person.SortName != "Vutu Doci" || person.Kind.String != "Person" {
		t.Errorf("person = %q/%q/%q, want Vutu Doci/Vutu Doci/Person", person.Name, person.SortName, person.Kind.String)
	}
}

// An unverified album's folder artist is the identified artist of that name, not a second person (D6).
func TestResolveFolderArtist_JoinsTheOneIdentifiedPersonOfThatName(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)
	identified := resolvePersonNamed(t, people, "artist-1", "Vutu Doci")

	joined, err := people.ResolveFolderArtist(t.Context(), firstSeen, "local:artist", "vutu doci")
	if err != nil {
		t.Fatalf("ResolveFolderArtist: %v", err)
	}
	unknown, err := people.ResolveFolderArtist(t.Context(), firstSeen, "local:artist", "Katupoken")
	if err != nil {
		t.Fatalf("ResolveFolderArtist: %v", err)
	}
	again, err := people.ResolveFolderArtist(t.Context(), firstSeen, "local:artist", "Katupoken")
	if err != nil {
		t.Fatalf("ResolveFolderArtist: %v", err)
	}
	if joined != identified || unknown == identified || again != unknown {
		t.Errorf("ids = joined %d, unknown %d, again %d; want joined %d, one new local person reused", joined, unknown, again, identified)
	}
}

// An artist renamed upstream is still found by the name its old folder carries (M1g 1.1).
func TestResolveFolderArtist_JoinsThePersonByAnAlias(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)
	identified := resolvePersonNamed(t, people, "artist-1", "Vutu Doci")
	if err := people.BackfillDetails(t.Context(), identified, library.PersonIdentity{
		Name: "Vutu Doci", Aliases: []string{"Vudu Doci", " "},
	}); err != nil {
		t.Fatalf("BackfillDetails: %v", err)
	}

	joined, err := people.ResolveFolderArtist(t.Context(), firstSeen, "local:artist", "VUDU doci")
	if err != nil {
		t.Fatalf("ResolveFolderArtist: %v", err)
	}
	aliases, err := librarydb.New(opened.Read()).ListAliasesForPerson(t.Context(), identified)
	if err != nil {
		t.Fatalf("ListAliasesForPerson: %v", err)
	}
	if joined != identified || len(aliases) != 1 || aliases[0] != "Vudu Doci" {
		t.Errorf("joined %d, aliases %v; want person %d and [Vudu Doci]", joined, aliases, identified)
	}
}
