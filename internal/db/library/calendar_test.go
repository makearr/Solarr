package library_test

import (
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

	entries, err := library.NewCalendar(opened).Entries(t.Context(), "2026-01-01", "2026-12-31", 50)
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
		Person: library.PersonIdentity{Name: "Someone", SortName: "Someone"},
	})
	if err != nil {
		t.Fatalf("ResolvePerson: %v", err)
	}
	if err := people.AddCredit(t.Context(), work.ID, personID, "artist", 0); err != nil {
		t.Fatalf("AddCredit: %v", err)
	}

	before, err := library.NewCalendar(opened).Entries(t.Context(), "2026-01-01", "2026-12-31", 50)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(before) != 0 {
		t.Fatalf("entries = %+v, want none while the artist is unmonitored", before)
	}

	if _, err := people.Subscribe(t.Context(), firstSeen, personID, 1, library.SubscriptionRequest{
		MediaTypes: []string{"music"},
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	after, err := library.NewCalendar(opened).Entries(t.Context(), "2026-01-01", "2026-12-31", 50)
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

	entries, err := library.NewCalendar(opened).Entries(t.Context(), "2026-03-01", "2026-12-31", 50)
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
