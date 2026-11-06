package library_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// autoEvent stores an event the way an older pass did: dense order, no part count.
func autoEvent(t *testing.T, opened *store.Store, name string, partIDs ...int64) int64 {
	t.Helper()
	var id int64
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		created, err := q.CreateCollection(ctx, librarydb.CreateCollectionParams{MediaType: "tv", Name: name, Kind: library.CrossoverKind, Source: library.AutoCrossoverSource, AddedAt: 1})
		if err != nil {
			return err
		}
		id = created.ID
		for ord, partID := range partIDs {
			part, err := q.GetWorkPart(ctx, partID)
			if err != nil {
				return err
			}
			if err := q.AddCollectionMember(ctx, librarydb.AddCollectionMemberParams{CollectionID: id, WorkID: part.WorkID, PartID: partID, Ord: sql.NullInt64{Int64: int64(ord + 1), Valid: true}}); err != nil {
				return err
			}
		}
		return nil
	})
	return id
}

func linkProviderID(t *testing.T, opened *store.Store, workID int64, provider, externalID string) {
	t.Helper()
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.LinkWorkExternalID(ctx, librarydb.LinkWorkExternalIDParams{Provider: provider, ExternalID: externalID, WorkID: workID, Confidence: 1, FirstSeenAt: 1})
	})
}

type shown struct {
	Title   string
	Number  int
	Guessed bool
}

func shownMembers(event library.Crossover) []shown {
	out := make([]shown, len(event.Members))
	for i, member := range event.Members {
		out[i] = shown{member.PartTitle, member.Number, member.Guessed}
	}
	return out
}

func TestMergeSplitEventsMakesOneEventOfTheHalvesOfTheSameName(t *testing.T) {
	opened, _ := newLibrary(t)
	crossovers := library.NewCrossovers(opened)
	var parts [4]int64
	for i, title := range []string{"Great Night (1)", "Great Night (2)", "Great Night (3)", "Great Night (4)"} {
		work := createWork(t, opened, "tv", "Invented Series "+string(rune('A'+i)))
		parts[i] = createPart(t, opened, work.ID, "s01:e01", "s01:e01", title).ID
	}
	dateParts(t, opened, map[int64]string{parts[0]: "2017-11-27", parts[1]: "2017-11-28", parts[2]: "2017-11-29", parts[3]: "2017-11-30"})
	autoEvent(t, opened, "Great Night", parts[1], parts[3])
	autoEvent(t, opened, "Great Night", parts[0], parts[2])

	merged, err := crossovers.MergeSplitEvents(t.Context(), "tv")
	if err != nil || merged != 1 {
		t.Fatalf("merged = %d, %v; want 1", merged, err)
	}
	events, err := crossovers.List(t.Context(), "tv")
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %+v, %v; want one", events, err)
	}
	want := []shown{{"Great Night (1)", 1, false}, {"Great Night (2)", 2, false}, {"Great Night (3)", 3, false}, {"Great Night (4)", 4, false}}
	if got := shownMembers(events[0]); !reflect.DeepEqual(got, want) || events[0].Total != 4 || events[0].Gaps != nil {
		t.Errorf("event = %+v, total %d, gaps %v; want %+v, 4, none", got, events[0].Total, events[0].Gaps, want)
	}
}

func TestMergeSplitEventsKeepsEventsOfTheSameNameApartYearsApart(t *testing.T) {
	opened, _ := newLibrary(t)
	crossovers := library.NewCrossovers(opened)
	var parts [4]int64
	for i := range parts {
		work := createWork(t, opened, "tv", "Invented Series "+string(rune('A'+i)))
		parts[i] = createPart(t, opened, work.ID, "s01:e01", "s01:e01", "Great Night").ID
	}
	dateParts(t, opened, map[int64]string{parts[0]: "2010-01-01", parts[1]: "2010-01-02", parts[2]: "2019-01-01", parts[3]: "2019-01-02"})
	autoEvent(t, opened, "Great Night", parts[0], parts[1])
	autoEvent(t, opened, "Great Night", parts[2], parts[3])
	if merged, err := crossovers.MergeSplitEvents(t.Context(), "tv"); err != nil || merged != 0 {
		t.Errorf("merged = %d, %v; want 0", merged, err)
	}
}

func TestDetectEventsSlotsAnUnnumberedEpisodeIntoTheGapAndFlagsIt(t *testing.T) {
	opened, _ := newLibrary(t)
	crossovers := library.NewCrossovers(opened)
	franchises := library.NewFranchises(opened)
	titles := []string{"Great Night (1)", "", "Great Night (3)"}
	var parts [3]int64
	for i, title := range []string{"Invented Show", "Invented Spinoff", "Invented Sequel"} {
		work := createWork(t, opened, "tv", title)
		createDesiredInCategory(t, opened, work.ID, 7)
		if _, err := franchises.Join(t.Context(), "tv", "wikidata", "Q1", "Invented Universe", work.ID); err != nil {
			t.Fatal(err)
		}
		heading := titles[i]
		if heading == "" {
			heading = "Great Night"
		}
		parts[i] = createPart(t, opened, work.ID, "s01:e01", "s01:e01", heading).ID
	}
	dateParts(t, opened, map[int64]string{parts[0]: "2018-12-09", parts[1]: "2018-12-10", parts[2]: "2018-12-11"})
	if made, err := crossovers.DetectEvents(t.Context(), "tv", 7); err != nil || made != 1 {
		t.Fatalf("made = %d, %v; want 1", made, err)
	}
	events, err := crossovers.List(t.Context(), "tv")
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %+v, %v", events, err)
	}
	want := []shown{{"Great Night (1)", 1, false}, {"Great Night", 2, true}, {"Great Night (3)", 3, false}}
	if got := shownMembers(events[0]); !reflect.DeepEqual(got, want) {
		t.Errorf("members = %+v; want %+v", got, want)
	}
}

func TestDetectEventsAddsALateEpisodeToTheEventFoundBefore(t *testing.T) {
	opened, _ := newLibrary(t)
	crossovers := library.NewCrossovers(opened)
	franchises := library.NewFranchises(opened)
	var parts [3]int64
	for i, title := range []string{"Great Night (1)", "Great Night (2)", "Great Night (3)"} {
		work := createWork(t, opened, "tv", "Invented Series "+string(rune('A'+i)))
		createDesiredInCategory(t, opened, work.ID, 7)
		if _, err := franchises.Join(t.Context(), "tv", "wikidata", "Q1", "Invented Universe", work.ID); err != nil {
			t.Fatal(err)
		}
		parts[i] = createPart(t, opened, work.ID, "s01:e01", "s01:e01", title).ID
	}
	dateParts(t, opened, map[int64]string{parts[0]: "2018-12-09", parts[1]: "2018-12-10", parts[2]: "2018-12-11"})
	autoEvent(t, opened, "Great Night", parts[0], parts[1])
	if _, err := crossovers.DetectEvents(t.Context(), "tv", 7); err != nil {
		t.Fatal(err)
	}
	events, err := crossovers.List(t.Context(), "tv")
	if err != nil || len(events) != 1 || len(events[0].Members) != 3 || events[0].Total != 3 {
		t.Fatalf("events = %+v, %v; want the one event grown to three parts", events, err)
	}
}

func TestRecordProviderEventListsThePartsOfSeriesTheLibraryLacks(t *testing.T) {
	opened, _ := newLibrary(t)
	crossovers := library.NewCrossovers(opened)
	first := createWork(t, opened, "tv", "Invented Show")
	second := createWork(t, opened, "tv", "Invented Spinoff")
	linkProviderID(t, opened, first.ID, "tvdb", "100")
	linkProviderID(t, opened, second.ID, "tvdb", "200")
	one := createPart(t, opened, first.ID, "s03:e09", "s03:e09", "Great Night (1)")
	createPart(t, opened, second.ID, "s01:e08", "s01:e08", "Great Night (3)")
	event := library.ProviderEvent{Name: "Great Night", Total: 3, Parts: []library.ProviderEventPart{
		{Number: 1, Provider: "tvdb", ExternalID: "100", SeriesTitle: "Invented Show", Season: 3, Episode: 9, Title: "Great Night (1)", Date: "2018-12-09"},
		{Provider: "tvdb", ExternalID: "300", SeriesTitle: "Invented Sequel", Season: 1, Episode: 2, Title: "Great Night", Date: "2018-12-10"},
		{Number: 3, Provider: "tvdb", ExternalID: "200", SeriesTitle: "Invented Spinoff", Season: 1, Episode: 8, Title: "Great Night (3)", Date: "2018-12-11"},
	}}
	recorded, err := crossovers.RecordProviderEvent(t.Context(), "tv", event)
	if err != nil || !recorded {
		t.Fatalf("recorded = %v, %v; want true", recorded, err)
	}
	events, err := crossovers.List(t.Context(), "tv")
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %+v, %v", events, err)
	}
	wantMissing := []library.MissingPart{{Number: 2, Provider: "tvdb", ExternalID: "300", SeriesTitle: "Invented Sequel", Season: 1, Episode: 2, Title: "Great Night", Date: "2018-12-10"}}
	if got := shownMembers(events[0]); !reflect.DeepEqual(got, []shown{{"Great Night (1)", 1, false}, {"Great Night (3)", 3, false}}) ||
		events[0].Total != 3 || !reflect.DeepEqual(events[0].Missing, wantMissing) || events[0].Gaps != nil {
		t.Errorf("event = %+v; want two members, total 3, the sequel's episode missing at part 2 (series not in the library)", events[0])
	}
	if events[0].Members[0].PartID != one.ID {
		t.Errorf("first member = part %d, want %d", events[0].Members[0].PartID, one.ID)
	}

	if again, err := crossovers.RecordProviderEvent(t.Context(), "tv", event); err != nil || !again {
		t.Fatalf("second record = %v, %v", again, err)
	}
	if events, err = crossovers.List(t.Context(), "tv"); err != nil || len(events) != 1 || len(events[0].Missing) != 1 {
		t.Errorf("after recording twice events = %+v, %v; want the one event unchanged", events, err)
	}
}

func TestRecordProviderEventDoesNotRecordWhatTheLibraryHoldsNoPartOf(t *testing.T) {
	opened, _ := newLibrary(t)
	crossovers := library.NewCrossovers(opened)
	event := library.ProviderEvent{Name: "Great Night", Parts: []library.ProviderEventPart{
		{Number: 1, Provider: "tvdb", ExternalID: "100", SeriesTitle: "Invented Show", Season: 1, Episode: 1, Title: "Great Night (1)", Date: "2018-12-09"},
		{Number: 2, Provider: "tvdb", ExternalID: "200", SeriesTitle: "Invented Spinoff", Season: 1, Episode: 1, Title: "Great Night (2)", Date: "2018-12-10"},
	}}
	if recorded, err := crossovers.RecordProviderEvent(t.Context(), "tv", event); err != nil || recorded {
		t.Errorf("recorded = %v, %v; want false", recorded, err)
	}
}

func TestADeletedProviderEventIsNotRecordedAgain(t *testing.T) {
	opened, _ := newLibrary(t)
	crossovers := library.NewCrossovers(opened)
	first := createWork(t, opened, "tv", "Invented Show")
	linkProviderID(t, opened, first.ID, "tvdb", "100")
	createPart(t, opened, first.ID, "s01:e01", "s01:e01", "Great Night (1)")
	event := library.ProviderEvent{Name: "Great Night", Total: 2, Parts: []library.ProviderEventPart{
		{Number: 1, Provider: "tvdb", ExternalID: "100", SeriesTitle: "Invented Show", Season: 1, Episode: 1, Title: "Great Night (1)", Date: "2018-12-09"},
		{Number: 2, Provider: "tvdb", ExternalID: "200", SeriesTitle: "Invented Spinoff", Season: 1, Episode: 1, Title: "Great Night (2)", Date: "2018-12-10"},
	}}
	if recorded, err := crossovers.RecordProviderEvent(t.Context(), "tv", event); err != nil || !recorded {
		t.Fatalf("recorded = %v, %v", recorded, err)
	}
	events, _ := crossovers.List(t.Context(), "tv")
	if err := crossovers.Delete(t.Context(), events[0].ID); err != nil {
		t.Fatal(err)
	}
	if recorded, err := crossovers.RecordProviderEvent(t.Context(), "tv", event); err != nil || recorded {
		t.Errorf("after deletion recorded = %v, %v; want false", recorded, err)
	}
}

func TestMarkNotSameKeepsAPairOutOfTheSuggestionsAndOfLinking(t *testing.T) {
	opened, _ := newLibrary(t)
	crossovers := library.NewCrossovers(opened)
	first := createWork(t, opened, "tv", "Invented Show")
	second := createWork(t, opened, "tv", "Invented Spinoff")
	createDesiredInCategory(t, opened, first.ID, 7)
	createDesiredInCategory(t, opened, second.ID, 7)
	a := createPart(t, opened, first.ID, "s01:e01", "s01:e01", "A Night Out")
	b := createPart(t, opened, second.ID, "s02:e03", "s02:e03", "A Night Out")
	dateParts(t, opened, map[int64]string{a.ID: "2018-12-09", b.ID: "2018-12-09"})

	suggestions, err := crossovers.Suggestions(t.Context(), "tv", 7, 10)
	if err != nil || len(suggestions) != 1 {
		t.Fatalf("suggestions = %+v, %v; want one", suggestions, err)
	}
	if err := crossovers.MarkNotSame(t.Context(), b.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	if suggestions, err = crossovers.Suggestions(t.Context(), "tv", 7, 10); err != nil || len(suggestions) != 0 {
		t.Errorf("after not the same suggestions = %+v, %v; want none", suggestions, err)
	}
	// The answer is about the episodes, so it holds when the parts are rebuilt under the same references.
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error { return q.DeleteWorkPart(ctx, b.ID) })
	b = createPart(t, opened, second.ID, "s02:e03", "s02:e03", "A Night Out")
	dateParts(t, opened, map[int64]string{b.ID: "2018-12-09"})
	if suggestions, err = crossovers.Suggestions(t.Context(), "tv", 7, 10); err != nil || len(suggestions) != 0 {
		t.Errorf("after the part was rebuilt suggestions = %+v, %v; want none", suggestions, err)
	}
}
