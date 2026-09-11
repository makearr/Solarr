package library_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/makearr/MediaNexus/internal/db/library"
	"github.com/makearr/MediaNexus/internal/db/store/gen/librarydb"
)

func resolvePersonNamed(t *testing.T, people library.People, externalID, name, sortName string) int64 {
	t.Helper()
	id, err := people.ResolvePerson(t.Context(), firstSeen, library.PersonLink{
		Provider: "musicbrainz:artist", ExternalID: externalID,
		Person: library.PersonIdentity{Name: name, SortName: sortName},
	})
	if err != nil {
		t.Fatalf("ResolvePerson(%q): %v", name, err)
	}
	return id
}

func TestListPeople_OrdersBySortNameThenID(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)
	resolvePersonNamed(t, people, "artist-1", "Katupoken", "Katupoken")
	resolvePersonNamed(t, people, "artist-2", "Vutu Doci", "Bush, Vutu")
	resolvePersonNamed(t, people, "artist-3", "CEHA", "CEHA")

	got, err := people.ListPeople(t.Context(), library.PersonFilter{}, library.PersonCursor{}, 50)
	if err != nil {
		t.Fatalf("ListPeople: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("ListPeople = %+v, want 3 people", got)
	}
	if got[0].SortName != "CEHA" || got[1].SortName != "Bush, Vutu" || got[2].SortName != "Katupoken" {
		t.Errorf("order = %q, %q, %q, want CEHA, Bush Vutu, Katupoken by sort_name", got[0].SortName, got[1].SortName, got[2].SortName)
	}
}

// ADR-54's Artists level: only persons credited role="artist" on a work
// visible to the category appear, and a person credited only in a role that
// isn't "artist" (a producer, say) never does.
func TestListPeople_RoleAndVisibilityFilterNarrowsToArtistsInTheCategory(t *testing.T) {
	opened, _ := newLibrary(t)
	identity := library.NewIdentity(opened)
	people := library.NewPeople(opened)

	visibleWork, err := identity.ResolveWorks(t.Context(), firstSeen, []library.WorkLink{{
		Provider: "test", ExternalID: "a", Confidence: 1,
		Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: "A", SortTitle: "a"}, RootFolderID: 1,
	}})
	if err != nil {
		t.Fatalf("ResolveWorks (visible): %v", err)
	}
	otherRootWork, err := identity.ResolveWorks(t.Context(), firstSeen, []library.WorkLink{{
		Provider: "test", ExternalID: "b", Confidence: 1,
		Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: "B", SortTitle: "b"}, RootFolderID: 2,
	}})
	if err != nil {
		t.Fatalf("ResolveWorks (other root): %v", err)
	}

	artistInCategory := resolvePersonNamed(t, people, "artist-in", "In Category", "In Category")
	artistElsewhere := resolvePersonNamed(t, people, "artist-out", "Out Of Category", "Out Of Category")
	producerOnly := resolvePersonNamed(t, people, "producer-only", "Producer Only", "Producer Only")

	if err := people.AddCredit(t.Context(), visibleWork[0], artistInCategory, "artist", 0); err != nil {
		t.Fatalf("AddCredit (in category): %v", err)
	}
	if err := people.AddCredit(t.Context(), otherRootWork[0], artistElsewhere, "artist", 0); err != nil {
		t.Fatalf("AddCredit (other root): %v", err)
	}
	if err := people.AddCredit(t.Context(), visibleWork[0], producerOnly, "producer", 0); err != nil {
		t.Fatalf("AddCredit (producer): %v", err)
	}

	got, err := people.ListPeople(t.Context(),
		library.PersonFilter{Role: "artist", RootFolderIDs: []int64{1}}, library.PersonCursor{}, 50)
	if err != nil {
		t.Fatalf("ListPeople: %v", err)
	}
	if len(got) != 1 || got[0].ID != artistInCategory {
		t.Errorf("ListPeople = %+v, want exactly the one artist credited in the visible root", got)
	}
}

// Unlike TrackParts' own plan test (bench_test.go, corpus-scale, since two
// viable indexes competed there), person_sort is the only index that could
// ever serve this ORDER BY -- there is no competing candidate for a small
// table to pick "correctly" by accident, so the EXISTS-vs-TEMP-B-TREE
// question this asks is scale-independent (ADR-28; see notes.md).
func TestListPeople_ArtistFilterPlanReachesTheIndexAndNeverSorts(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)

	plan, err := people.ExplainPeopleList(t.Context(),
		library.PersonFilter{Role: "artist", RootFolderIDs: []int64{1}}, library.PersonCursor{}, 50)
	if err != nil {
		t.Fatalf("ExplainPeopleList: %v", err)
	}
	if !strings.Contains(plan, "person_sort") {
		t.Errorf("plan does not reach person_sort:\n%s", plan)
	}
	if strings.Contains(plan, "TEMP B-TREE") {
		t.Errorf("plan sorts the whole result:\n%s", plan)
	}
}

func TestListPeople_MonitoredFilterNarrows(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)
	unmonitoredID := resolvePersonNamed(t, people, "artist-1", "Katupoken", "Katupoken")
	monitoredID := resolvePersonNamed(t, people, "artist-2", "Vutu Doci", "Bush, Vutu")
	if _, err := people.Subscribe(t.Context(), firstSeen, monitoredID, 1,
		library.SubscriptionRequest{MediaTypes: []string{"music"}}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	monitored := true
	got, err := people.ListPeople(t.Context(), library.PersonFilter{Monitored: &monitored}, library.PersonCursor{}, 50)
	if err != nil {
		t.Fatalf("ListPeople: %v", err)
	}
	if len(got) != 1 || got[0].ID != monitoredID {
		t.Errorf("ListPeople(monitored) = %+v, want only person %d", got, monitoredID)
	}
	_ = unmonitoredID
}

func TestListPeople_NamePrefixNarrows(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)
	resolvePersonNamed(t, people, "artist-1", "Katupoken", "Katupoken")
	resolvePersonNamed(t, people, "artist-2", "Vutu Doci", "Bush, Vutu")

	got, err := people.ListPeople(t.Context(), library.PersonFilter{NamePrefix: "Rad"}, library.PersonCursor{}, 50)
	if err != nil {
		t.Fatalf("ListPeople: %v", err)
	}
	if len(got) != 1 || got[0].SortName != "Katupoken" {
		t.Errorf("ListPeople(prefix Rad) = %+v, want only Katupoken", got)
	}
}

func TestPeopleDetail_IncludesAliasesBiographyAndDiscography(t *testing.T) {
	opened, queries := newLibrary(t)
	identity := library.NewIdentity(opened)
	people := library.NewPeople(opened)

	personID, err := people.ResolvePerson(t.Context(), firstSeen, library.PersonLink{
		Provider: "musicbrainz:artist", ExternalID: "artist-1",
		Person: library.PersonIdentity{Name: "Nick Cave", SortName: "Cave, Nick", Biography: "Australian musician."},
	})
	if err != nil {
		t.Fatalf("ResolvePerson: %v", err)
	}
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.AddPersonAlias(ctx, librarydb.AddPersonAliasParams{
			PersonID: personID, Alias: "Nick Cave and the Bad Seeds", Normalized: "nick cave and the bad seeds", Source: "musicbrainz",
		})
	})
	workID := newWorkForCredits(t, identity, queries, "rg-detail-1")
	if err := people.AddCredit(t.Context(), workID, personID, "artist", 0); err != nil {
		t.Fatalf("AddCredit: %v", err)
	}

	detail, err := people.Detail(t.Context(), personID)
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if detail.Person.Name != "Nick Cave" {
		t.Errorf("Person.Name = %q, want Nick Cave", detail.Person.Name)
	}
	if detail.Biography != "Australian musician." {
		t.Errorf("Biography = %q, want the decoded biography", detail.Biography)
	}
	if len(detail.Aliases) != 1 || detail.Aliases[0] != "Nick Cave and the Bad Seeds" {
		t.Errorf("Aliases = %+v, want the one alias", detail.Aliases)
	}
	if len(detail.Discography) != 1 || detail.Discography[0].ID != workID {
		t.Errorf("Discography = %+v, want the one credited work", detail.Discography)
	}
}

func TestSubscribe_MissingPersonReportsNoRows(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)

	_, err := people.Subscribe(t.Context(), firstSeen, 404, 1,
		library.SubscriptionRequest{MediaTypes: []string{"music"}})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("Subscribe to a missing person = %v, want an error wrapping sql.ErrNoRows", err)
	}
}

func TestSubscribe_MarksThePersonMonitored(t *testing.T) {
	opened, queries := newLibrary(t)
	people := library.NewPeople(opened)
	personID := resolvePersonNamed(t, people, "artist-1", "Vutu Doci", "Bush, Vutu")

	before, err := queries.GetPerson(t.Context(), personID)
	if err != nil {
		t.Fatalf("GetPerson: %v", err)
	}
	if before.Monitored != 0 {
		t.Fatal("a newly resolved person is already monitored, want false")
	}

	if _, err := people.Subscribe(t.Context(), firstSeen, personID, 1,
		library.SubscriptionRequest{MediaTypes: []string{"music"}, Roles: []string{"artist"}}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	after, err := queries.GetPerson(t.Context(), personID)
	if err != nil {
		t.Fatalf("GetPerson: %v", err)
	}
	if after.Monitored != 1 {
		t.Error("Monitored = false after Subscribe, want true (ADR-32: subscribing monitors them)")
	}
}

// A second Subscribe for the same person and user must return the existing
// subscription, not error on the UNIQUE(subject_type, subject_id, user_id)
// constraint or create a duplicate row (Invariant 80) -- found needed live
// while wiring the Lidarr importer's own re-run: a re-import subscribing an
// artist a second time must be a no-op, the same idempotent shape
// ResolvePerson and Desired.EnsureAlbumItem already have.
func TestSubscribe_SecondCallForTheSamePersonIsIdempotent(t *testing.T) {
	opened, queries := newLibrary(t)
	people := library.NewPeople(opened)
	personID := resolvePersonNamed(t, people, "artist-1", "Vutu Doci", "Bush, Vutu")

	first, err := people.Subscribe(t.Context(), firstSeen, personID, 1,
		library.SubscriptionRequest{MediaTypes: []string{"music"}, Roles: []string{"artist"}})
	if err != nil {
		t.Fatalf("first Subscribe: %v", err)
	}

	second, err := people.Subscribe(t.Context(), firstSeen, personID, 1,
		library.SubscriptionRequest{MediaTypes: []string{"music"}, Roles: []string{"artist"}})
	if err != nil {
		t.Fatalf("second Subscribe: %v", err)
	}
	if second != first {
		t.Errorf("second Subscribe returned id %d, want the same id %d as the first", second, first)
	}

	rows, err := queries.ListEnabledSubscriptions(t.Context())
	if err != nil {
		t.Fatalf("ListEnabledSubscriptions: %v", err)
	}
	count := 0
	for _, row := range rows {
		if row.SubjectType == "person" && row.SubjectID == personID {
			count++
		}
	}
	if count != 1 {
		t.Errorf("subscription rows for person %d = %d, want exactly 1", personID, count)
	}
}
