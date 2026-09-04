package library_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/makearr/MediaNexus/internal/library"
	"github.com/makearr/MediaNexus/internal/store/gen/librarydb"
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
