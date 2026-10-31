package library_test

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

func setDate(t *testing.T, queries *librarydb.Queries, workID int64, date string) {
	t.Helper()
	if err := queries.SetWorkDate(t.Context(), librarydb.SetWorkDateParams{
		WorkID: workID, Kind: library.WorkDateStreet, Region: "", Date: date, Source: "test",
	}); err != nil {
		t.Fatalf("SetWorkDate: %v", err)
	}
}

// A calendar of everything the provider knows would be noise. Only works this
// library has a reason to care about appear: something wants it, or a
// monitored artist is credited on it.
func TestCalendar_OnlyShowsWorksTheLibraryCaresAbout(t *testing.T) {
	opened, queries := newLibrary(t)
	wanted := createWork(t, opened, "music", "Wanted Album")
	ignored := createWork(t, opened, "music", "Unrelated Album")
	setDate(t, queries, wanted.ID, "2026-03-15")
	setDate(t, queries, ignored.ID, "2026-03-16")
	createDesired(t, opened, wanted.ID, "SET", 1)

	entries, err := library.NewCalendar(opened).Entries(t.Context(), "2026-01-01", "2026-12-31", []int64{1}, 50)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(entries) != 1 || entries[0].WorkID != wanted.ID {
		t.Fatalf("entries = %+v, want only the wanted album", entries)
	}
	if entries[0].ReleaseDate != "2026-03-15" {
		t.Errorf("ReleaseDate = %q, want 2026-03-15", entries[0].ReleaseDate)
	}
}

// A release credited to a monitored artist counts even with nothing wanting
// it yet: that is the whole point of following someone.
func TestCalendar_IncludesAMonitoredArtistsRelease(t *testing.T) {
	opened, queries := newLibrary(t)
	work := createWork(t, opened, "music", "Upcoming Album")
	setDate(t, queries, work.ID, "2026-04-01")

	people := library.NewPeople(opened)
	personID, err := people.ResolvePerson(t.Context(), firstSeen, library.PersonLink{
		Provider: "musicbrainz:artist", ExternalID: "artist-1",
		Person: library.PersonIdentity{Name: "Someone"},
	})
	if err != nil {
		t.Fatalf("ResolvePerson: %v", err)
	}
	if err := people.AddLeadCredit(t.Context(), work.ID, personID, "artist"); err != nil {
		t.Fatalf("AddCredit: %v", err)
	}

	before, err := library.NewCalendar(opened).Entries(t.Context(), "2026-01-01", "2026-12-31", []int64{1}, 50)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(before) != 0 {
		t.Fatalf("entries = %+v, want none while the artist is unmonitored", before)
	}

	if _, err := people.Subscribe(t.Context(), firstSeen, personID, 1, library.SubscriptionRequest{
		MediaTypes: []string{"music"}, CategoryID: 1,
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	after, err := library.NewCalendar(opened).Entries(t.Context(), "2026-01-01", "2026-12-31", []int64{1}, 50)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(after) != 1 || after[0].WorkID != work.ID {
		t.Errorf("entries = %+v, want the monitored artist's release", after)
	}
}

// The window is inclusive at both ends and compares ISO strings, so a partial
// date sits in the window it belongs to without a day being invented for it.
func TestCalendar_WindowIsInclusiveAndAcceptsPartialDates(t *testing.T) {
	opened, queries := newLibrary(t)
	edge := createWork(t, opened, "music", "Edge Album")
	partial := createWork(t, opened, "music", "Partial Album")
	outside := createWork(t, opened, "music", "Outside Album")
	setDate(t, queries, edge.ID, "2026-03-01")
	setDate(t, queries, partial.ID, "2026-04")
	setDate(t, queries, outside.ID, "2027-01-01")
	for _, work := range []int64{edge.ID, partial.ID, outside.ID} {
		createDesired(t, opened, work, "SET", 1)
	}

	entries, err := library.NewCalendar(opened).Entries(t.Context(), "2026-03-01", "2026-12-31", []int64{1}, 50)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %+v, want the edge and the partial date, not the one outside", entries)
	}
	if entries[0].ReleaseDate != "2026-03-01" || entries[1].ReleaseDate != "2026-04" {
		t.Errorf("entries = %+v, want them earliest first with the partial date as given", entries)
	}
}

// A calendar shows only libraries the caller can see (2026-09-19 review): a wanted album through
// its desired item's library, a followed artist's release through the subscription's.
func TestCalendar_OnlyShowsTheCallersLibraries(t *testing.T) {
	opened, queries := newLibrary(t)
	wanted := createWork(t, opened, "music", "Wanted In Library 2")
	followed := createWork(t, opened, "music", "Followed In Library 2")
	setDate(t, queries, wanted.ID, "2026-05-01")
	setDate(t, queries, followed.ID, "2026-05-02")
	createDesiredInCategory(t, opened, wanted.ID, 2)

	people := library.NewPeople(opened)
	personID, err := people.ResolvePerson(t.Context(), firstSeen, library.PersonLink{
		Provider: "musicbrainz:artist", ExternalID: "artist-2",
		Person: library.PersonIdentity{Name: "Someone Else"},
	})
	if err != nil {
		t.Fatalf("ResolvePerson: %v", err)
	}
	if err := people.AddLeadCredit(t.Context(), followed.ID, personID, "artist"); err != nil {
		t.Fatalf("AddCredit: %v", err)
	}
	if _, err := people.Subscribe(t.Context(), firstSeen, personID, 1, library.SubscriptionRequest{
		MediaTypes: []string{"music"}, CategoryID: 2,
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	calendar := library.NewCalendar(opened)
	hidden, err := calendar.Entries(t.Context(), "2026-01-01", "2026-12-31", []int64{1}, 50)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(hidden) != 0 {
		t.Errorf("a caller seeing only library 1 got %+v", hidden)
	}
	visible, err := calendar.Entries(t.Context(), "2026-01-01", "2026-12-31", []int64{1, 2}, 50)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(visible) != 2 {
		t.Errorf("a caller seeing library 2 got %+v, want both releases", visible)
	}
}

// The year view needs a count per date, with the same filter the list applies.
func TestCalendar_DatesCountTheSameReleases(t *testing.T) {
	opened, queries := newLibrary(t)
	for _, date := range []string{"2026-06-01", "2026-06-01", "2026-06"} {
		work := createWork(t, opened, "music", "Album "+date)
		setDate(t, queries, work.ID, date)
		createDesired(t, opened, work.ID, "SET", 1)
	}
	other := createWork(t, opened, "music", "Elsewhere")
	setDate(t, queries, other.ID, "2026-06-02")
	createDesiredInCategory(t, opened, other.ID, 2)

	dates, err := library.NewCalendar(opened).Dates(t.Context(), "2026-01-01", "2026-12-31", []int64{1})
	if err != nil {
		t.Fatalf("Dates: %v", err)
	}
	want := []library.CalendarDate{{Date: "2026-06", Releases: 1}, {Date: "2026-06-01", Releases: 2}}
	if !reflect.DeepEqual(dates, want) {
		t.Errorf("dates = %+v, want %+v", dates, want)
	}
}

// A film, a book or a scene stores its day as "release", not music's "street": the calendar lists it all the same.
func TestCalendar_ListsWhatTheOtherTypesCallTheirReleaseDay(t *testing.T) {
	opened, queries := newLibrary(t)
	scene := createWork(t, opened, "adult", "A Wanted Scene")
	if err := queries.SetWorkDate(t.Context(), librarydb.SetWorkDateParams{WorkID: scene.ID, Kind: "release", Date: "2026-10-25", Source: "test"}); err != nil {
		t.Fatal(err)
	}
	createDesired(t, opened, scene.ID, "SET", 1)

	calendar := library.NewCalendar(opened)
	entries, err := calendar.Entries(t.Context(), "2026-10-01", "2026-10-31", []int64{1}, 50)
	if err != nil || len(entries) != 1 || entries[0].WorkID != scene.ID || entries[0].ReleaseDate != "2026-10-25" {
		t.Fatalf("entries = %+v, %v; want the scene on its day", entries, err)
	}
	dates, err := calendar.Dates(t.Context(), "2026-10-01", "2026-10-31", []int64{1})
	if err != nil || len(dates) != 1 || dates[0].Date != "2026-10-25" || dates[0].Releases != 1 {
		t.Errorf("dates = %+v, %v", dates, err)
	}
}
