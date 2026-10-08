package library_test

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/platform/listfilter"
)

func resolvePersonNamed(t *testing.T, people library.People, externalID, name string) int64 {
	t.Helper()
	id, err := people.ResolvePerson(t.Context(), firstSeen, library.PersonLink{
		Provider: "musicbrainz:artist", ExternalID: externalID,
		Person: library.PersonIdentity{Name: name},
	})
	if err != nil {
		t.Fatalf("ResolvePerson(%q): %v", name, err)
	}
	return id
}

func TestListPeople_OrdersBySortNameThenID(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)
	resolvePersonNamed(t, people, "artist-1", "Katupoken")
	resolvePersonNamed(t, people, "artist-2", "Vutu Doci")
	resolvePersonNamed(t, people, "artist-3", "AVEX")

	got, err := people.List(t.Context(), library.PeopleFilter{}, library.PeopleCursor{}, 50)
	if err != nil {
		t.Fatalf("ListPeople: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("ListPeople = %+v, want 3 people", got)
	}
	// By the name as shown, as Lidarr: a provider's "Doci, Vutu" once filed Vutu Doci under D (2026-09-30).
	if got[0].SortName != "AVEX" || got[1].SortName != "Katupoken" || got[2].SortName != "Vutu Doci" {
		t.Errorf("order = %q, %q, %q, want AVEX, Katupoken, Vutu Doci by name", got[0].SortName, got[1].SortName, got[2].SortName)
	}
}

// Lidarr ignores case: "bokaru" sorts after "avex" and before "Ubra", on every page and either way.
func TestListPeople_OrderIgnoresCase(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)
	resolvePersonNamed(t, people, "artist-1", "Ubra")
	resolvePersonNamed(t, people, "artist-2", "bokaru")
	resolvePersonNamed(t, people, "artist-3", "Vutu Doci")
	resolvePersonNamed(t, people, "artist-4", "BOKARU")
	resolvePersonNamed(t, people, "artist-5", "avex")

	for _, descending := range []bool{false, true} {
		var got []string
		var after library.PeopleCursor
		for {
			page, err := people.List(t.Context(), library.PeopleFilter{Desc: descending}, after, 2)
			if err != nil {
				t.Fatalf("ListPeople: %v", err)
			}
			if len(page) == 0 {
				break
			}
			for _, person := range page {
				got = append(got, person.SortName)
			}
			last := page[len(page)-1]
			after = library.PeopleCursor{Name: last.SortName, ID: last.ID, Set: true}
		}
		want := []string{"avex", "bokaru", "BOKARU", "Ubra", "Vutu Doci"}
		if descending {
			slices.Reverse(want)
		}
		if !slices.Equal(got, want) {
			t.Errorf("descending %v: order = %q, want %q", descending, got, want)
		}
	}
}

// ADR-54's Artists level: only persons credited role="artist" on a work the
// library wants appear, and a person credited only in another role (a
// producer, say) never does.
func TestListPeople_RoleAndLibraryNarrowToArtistsOfTheLibrary(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)
	inLibrary := createWork(t, opened, "music", "A")
	elsewhere := createWork(t, opened, "music", "B")
	createDesiredInCategory(t, opened, inLibrary.ID, 1)
	createDesiredInCategory(t, opened, elsewhere.ID, 2)

	artistInCategory := resolvePersonNamed(t, people, "artist-in", "In Category")
	artistElsewhere := resolvePersonNamed(t, people, "artist-out", "Out Of Category")
	producerOnly := resolvePersonNamed(t, people, "producer-only", "Producer Only")
	for _, credit := range []struct {
		work, person int64
		role         string
	}{{inLibrary.ID, artistInCategory, "artist"}, {elsewhere.ID, artistElsewhere, "artist"}, {inLibrary.ID, producerOnly, "producer"}} {
		if err := people.AddCredit(t.Context(), credit.work, credit.person, credit.role, 0); err != nil {
			t.Fatalf("AddCredit: %v", err)
		}
	}

	got, err := people.List(t.Context(), library.PeopleFilter{Roles: []string{"artist"}, Category: 1}, library.PeopleCursor{}, 50)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].ID != artistInCategory || got[0].Works != 1 || !slices.Equal(got[0].Roles, []string{"artist"}) {
		t.Errorf("List = %+v, want exactly the one artist of library 1, with one work", got)
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

	plan, err := people.ExplainList(t.Context(),
		library.PeopleFilter{Roles: []string{"artist"}, Category: 1, Visible: []int64{1, 2}}, library.PeopleCursor{}, 50)
	if err != nil {
		t.Fatalf("ExplainList: %v", err)
	}
	if !strings.Contains(plan, "person_sort") {
		t.Errorf("plan does not reach person_sort:\n%s", plan)
	}
	if sortsOuterResult(plan) {
		t.Errorf("plan sorts the whole result:\n%s", plan)
	}
}

func TestListPeople_MonitoredFilterNarrows(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)
	unmonitoredID := resolvePersonNamed(t, people, "artist-1", "Katupoken")
	monitoredID := resolvePersonNamed(t, people, "artist-2", "Vutu Doci")
	if _, err := people.Subscribe(t.Context(), firstSeen, monitoredID, 1,
		library.SubscriptionRequest{MediaTypes: []string{"music"}}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	monitored := true
	got, err := people.List(t.Context(), library.PeopleFilter{Monitored: &monitored}, library.PeopleCursor{}, 50)
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
	resolvePersonNamed(t, people, "artist-1", "Katupoken")
	resolvePersonNamed(t, people, "artist-2", "Vutu Doci")

	got, err := people.List(t.Context(), library.PeopleFilter{NamePrefix: "Kat"}, library.PeopleCursor{}, 50)
	if err != nil {
		t.Fatalf("ListPeople: %v", err)
	}
	if len(got) != 1 || got[0].SortName != "Katupoken" {
		t.Errorf("ListPeople(prefix Kat) = %+v, want only Katupoken", got)
	}
}

func TestPeopleGet_IncludesAliasesBiographyAndLibraries(t *testing.T) {
	opened, queries := newLibrary(t)
	identity := library.NewIdentity(opened)
	people := library.NewPeople(opened)

	personID, err := people.ResolvePerson(t.Context(), firstSeen, library.PersonLink{
		Provider: "musicbrainz:artist", ExternalID: "artist-1",
		Person: library.PersonIdentity{Name: "Nick Cave", Biography: "Australian musician."},
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
	createDesiredInCategory(t, opened, workID, 3)
	if err := people.AddCredit(t.Context(), workID, personID, "artist", 0); err != nil {
		t.Fatalf("AddCredit: %v", err)
	}

	profile, err := people.Get(t.Context(), personID, []int64{3})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if profile.Name != "Nick Cave" || profile.Biography != "Australian musician." || profile.IDs["musicbrainz:artist"] != "artist-1" {
		t.Errorf("profile = %+v, want the name, the decoded biography and the artist id", profile)
	}
	if !slices.Equal(profile.Aliases, []string{"Nick Cave and the Bad Seeds"}) {
		t.Errorf("Aliases = %+v, want the one alias", profile.Aliases)
	}
	if want := []library.PersonLibrary{{CategoryID: 3, Role: "artist", Works: 1}}; !slices.Equal(profile.Libraries, want) {
		t.Errorf("Libraries = %+v, want %+v", profile.Libraries, want)
	}
	credits, err := people.Credits(t.Context(), personID, []int64{3})
	if err != nil || len(credits) != 1 || credits[0].ID != workID || credits[0].CategoryID != 3 || credits[0].Role != "artist" {
		t.Errorf("Credits = %+v, %v; want the one credited work in library 3", credits, err)
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
	personID := resolvePersonNamed(t, people, "artist-1", "Vutu Doci")

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

// FiltersJSON stays opaque here (todo 4.4) -- library only has to store and
// return it byte for byte, never parse it (a media type's own concern, ADR-23).
func TestSubscribe_FiltersJSONRoundTripsThroughListEnabled(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)
	subscriptions := library.NewSubscriptions(opened)
	personID := resolvePersonNamed(t, people, "artist-1", "Vutu Doci")

	if _, err := people.Subscribe(t.Context(), firstSeen, personID, 1, library.SubscriptionRequest{
		MediaTypes: []string{"music"}, FiltersJSON: `{"exclude_secondary_types":["live"]}`,
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	rows, err := subscriptions.ListEnabled(t.Context())
	if err != nil {
		t.Fatalf("ListEnabled: %v", err)
	}
	found := false
	for _, row := range rows {
		if row.SubjectID != personID {
			continue
		}
		found = true
		if row.FiltersJSON != `{"exclude_secondary_types":["live"]}` {
			t.Errorf("FiltersJSON = %q, want the exact JSON passed to Subscribe", row.FiltersJSON)
		}
	}
	if !found {
		t.Fatal("subscription not found in ListEnabled")
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
	personID := resolvePersonNamed(t, people, "artist-1", "Vutu Doci")

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

// The scheduled refresh takes monitored artists never refreshed first, then the stalest, and skips
// both the recently refreshed and the unmonitored.
func TestArtistsDueRefresh_StalestMonitoredFirst(t *testing.T) {
	opened, queries := newLibrary(t)
	people := library.NewPeople(opened)
	never := resolvePersonNamed(t, people, "a1", "Never")
	stale := resolvePersonNamed(t, people, "a2", "Stale")
	recent := resolvePersonNamed(t, people, "a3", "Recent")
	resolvePersonNamed(t, people, "a4", "Unmonitored")
	for _, id := range []int64{never, stale, recent} {
		if err := queries.SetPersonMonitored(t.Context(), librarydb.SetPersonMonitoredParams{Monitored: 1, ID: id}); err != nil {
			t.Fatal(err)
		}
	}
	if err := people.RecordRefresh(t.Context(), stale, 100); err != nil {
		t.Fatal(err)
	}
	if err := people.RecordRefresh(t.Context(), recent, 900); err != nil {
		t.Fatal(err)
	}

	due, err := people.ArtistsDueRefresh(t.Context(), 500, 10)
	if want := []int64{never, stale}; err != nil || !slices.Equal(due, want) {
		t.Errorf("due = %v, %v; want %v", due, err, want)
	}
}

// A name header clicked twice lists Z to A, across pages.
func TestListPeople_DescendingPagesZToA(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)
	resolvePersonNamed(t, people, "artist-1", "Katupoken")
	resolvePersonNamed(t, people, "artist-2", "Vutu Doci")
	resolvePersonNamed(t, people, "artist-3", "AVEX")

	var got []string
	var after library.PeopleCursor
	for {
		page, err := people.List(t.Context(), library.PeopleFilter{Desc: true}, after, 1)
		if err != nil {
			t.Fatalf("ListPeople: %v", err)
		}
		if len(page) == 0 {
			break
		}
		got = append(got, page[0].SortName)
		after = library.PeopleCursor{Name: page[0].SortName, ID: page[0].ID, Set: true}
	}
	if want := []string{"Vutu Doci", "Katupoken", "AVEX"}; !slices.Equal(got, want) {
		t.Errorf("order = %q, want %q", got, want)
	}
}

// Every field a people custom filter offers is valid SQL, and a condition narrows the list.
func TestPeopleList_CustomFilterConditions(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)
	fields := library.PersonFields([]string{"artist"})
	for field, spec := range fields {
		value := map[listfilter.Kind]string{listfilter.Text: "x", listfilter.Number: "1", listfilter.Bool: "true", listfilter.Enum: "artist"}[spec.Kind]
		op := map[listfilter.Kind]string{listfilter.Text: "contains", listfilter.Number: "ge", listfilter.Bool: "is", listfilter.Enum: "is"}[spec.Kind]
		conditions, err := listfilter.Parse(`[{"field":"`+field+`","op":"`+op+`","value":"`+value+`"}]`, fields)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := people.List(t.Context(), library.PeopleFilter{Where: conditions, Category: 1}, library.PeopleCursor{}, 10); err != nil {
			t.Errorf("field %s: %v", field, err)
		}
	}
}
