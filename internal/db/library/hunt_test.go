package library_test

import (
	"context"
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

func TestPlanInputsReadTheDirtySetWithReleaseDateAndSearchRecord(t *testing.T) {
	opened, _ := newLibrary(t)
	work := createWork(t, opened, "music", "Tender Prey")
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.SetWorkDate(ctx, librarydb.SetWorkDateParams{
			WorkID: work.ID, Kind: library.WorkDateStreet, Region: "", Date: "1988-05-02", Source: "test",
		})
	})
	item := createDesired(t, opened, work.ID, "SET", 12)
	hunt, desired := library.NewHunt(opened), library.NewDesired(opened)
	if err := desired.RecordSearched(t.Context(), item.ID, 4_000); err != nil {
		t.Fatalf("RecordSearched: %v", err)
	}

	got, err := hunt.PlanInputs(t.Context(), true, 0, 10, 0)
	if err != nil {
		t.Fatalf("PlanInputs: %v", err)
	}
	want := []library.PlanInput{{
		ID: item.ID, CategoryID: 1, Monitored: true, SatisfactionKind: "SET", State: "MISSING",
		ReleaseDate: "1988-05-02", LastSearchedAt: 4_000, SearchCount: 1, RatingAge: -1,
	}}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("PlanInputs = %+v, want %+v", got, want)
	}
}

func TestApplyPlanClearsDirtyAndKeepsTheSearchRecord(t *testing.T) {
	opened, queries := newLibrary(t)
	work := createWork(t, opened, "music", "Tender Prey")
	item := createDesired(t, opened, work.ID, "SET", 12)
	hunt, desired := library.NewHunt(opened), library.NewDesired(opened)
	if err := desired.RecordSearched(t.Context(), item.ID, 4_000); err != nil {
		t.Fatalf("RecordSearched: %v", err)
	}
	inputs, err := hunt.PlanInputs(t.Context(), true, 0, 10, 0)
	if err != nil {
		t.Fatalf("PlanInputs: %v", err)
	}

	applied, err := hunt.ApplyPlan(t.Context(), []library.PlannedHunt{{
		Input: inputs[0], Class: "missing", Priority: 19_900, EligibleAfter: 25_600, QueuedSince: 5_000,
	}}, 5_000)
	if err != nil || applied != 1 {
		t.Fatalf("ApplyPlan = %d, %v; want 1", applied, err)
	}
	rows, err := queries.NextHuntByOldestSearched(t.Context(),
		librarydb.NextHuntByOldestSearchedParams{Class: "missing", Now: 30_000, PageSize: 10})
	if err != nil {
		t.Fatalf("NextHuntByOldestSearched: %v", err)
	}
	want := librarydb.HuntQueue{
		DesiredItemID: item.ID, Class: "missing", Priority: 19_900, LastSearchedAt: 4_000,
		EligibleAfter: 25_600, SearchCount: 1, QueuedSince: 5_000,
	}
	if len(rows) != 1 || rows[0] != want {
		t.Fatalf("hunt rows = %+v, want %+v", rows, want)
	}
	if dirty, _ := hunt.PlanInputs(t.Context(), true, 0, 10, 0); len(dirty) != 0 {
		t.Fatalf("%d items still dirty after planning", len(dirty))
	}
}

// A state change between the read and the write means the plan is stale: it is not written.
func TestApplyPlanSkipsAnItemChangedSinceItWasRead(t *testing.T) {
	opened, queries := newLibrary(t)
	work := createWork(t, opened, "music", "Tender Prey")
	item := createDesired(t, opened, work.ID, "SET", 12)
	hunt := library.NewHunt(opened)
	inputs, err := hunt.PlanInputs(t.Context(), true, 0, 10, 0)
	if err != nil {
		t.Fatalf("PlanInputs: %v", err)
	}
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.SetDesiredItemState(ctx, librarydb.SetDesiredItemStateParams{State: "COMPLETE", PresentCount: 12, ID: item.ID})
	})

	applied, err := hunt.ApplyPlan(t.Context(), []library.PlannedHunt{{Input: inputs[0], Class: "missing", Priority: 1}}, 5_000)
	if err != nil || applied != 0 {
		t.Fatalf("ApplyPlan = %d, %v; want 0", applied, err)
	}
	if next, _ := hunt.NextToHunt(t.Context(), library.HuntSelection{Class: "missing", Mode: library.HuntOldestSearched, Now: 5_000, Limit: 10}); len(next) != 0 {
		t.Fatalf("a stale plan queued %v", next)
	}
	after, err := queries.GetDesiredItem(t.Context(), item.ID)
	if err != nil || after.Dirty != 1 {
		t.Fatalf("item dirty = %d, %v; want it left dirty for the next pass", after.Dirty, err)
	}
}

func TestPostponeKeepsTheHuntAwayUntilThen(t *testing.T) {
	opened, _ := newLibrary(t)
	work := createWork(t, opened, "music", "Tender Prey")
	item := createDesired(t, opened, work.ID, "SET", 12)
	hunt := library.NewHunt(opened)
	inputs, _ := hunt.PlanInputs(t.Context(), true, 0, 10, 0)
	if _, err := hunt.ApplyPlan(t.Context(), []library.PlannedHunt{{Input: inputs[0], Class: "missing"}}, 1); err != nil {
		t.Fatalf("ApplyPlan: %v", err)
	}
	if err := hunt.Postpone(t.Context(), item.ID, 9_000); err != nil {
		t.Fatalf("Postpone: %v", err)
	}
	for now, want := range map[int64]int{8_999: 0, 9_000: 1} {
		next, err := hunt.NextToHunt(t.Context(), library.HuntSelection{Class: "missing", Mode: library.HuntOldestSearched, Now: now, Limit: 10})
		if err != nil || len(next) != want {
			t.Errorf("NextToHunt at %d = %v, %v; want %d items", now, next, err, want)
		}
	}
}

// An RSS release by an artist's old name still finds the album wanted under its current one (M1g 1.1).
func TestWantedAlbumsByArtist_MatchesNameAndAlias(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)
	artist := resolvePersonNamed(t, people, "artist-1", "Vutu Doci")
	if err := people.BackfillDetails(t.Context(), artist, library.PersonIdentity{Name: "Vutu Doci", Aliases: []string{"Vudu Doci"}}); err != nil {
		t.Fatalf("BackfillDetails: %v", err)
	}
	work := createWork(t, opened, "music", "Album")
	if err := people.AddLeadCredit(t.Context(), work.ID, artist, "artist"); err != nil {
		t.Fatalf("AddCredit: %v", err)
	}
	item := createDesired(t, opened, work.ID, "SET", 10)

	hunt := library.NewHunt(opened)
	for _, name := range []string{"vutu doci", "VUDU DOCI"} {
		ids, err := hunt.WantedAlbumsByArtist(t.Context(), name, 10)
		if err != nil || len(ids) != 1 || ids[0] != item.ID {
			t.Errorf("WantedAlbumsByArtist(%q) = %v, %v; want [%d]", name, ids, err, item.ID)
		}
	}
	if ids, _ := hunt.WantedAlbumsByArtist(t.Context(), "Katupoken", 10); len(ids) != 0 {
		t.Errorf("an unrelated name matched %v", ids)
	}
}

// Section 9.3: each mode orders the eligible items its own way, and a release range narrows to new releases or gaps.
func TestNextToHunt_ModesAndReleaseRanges(t *testing.T) {
	opened, _ := newLibrary(t)
	hunt := library.NewHunt(opened)
	desired := library.NewDesired(opened)
	// searched[i] and released[i] are item i's last search and street date; the last has no date.
	searched := []int64{300, 100, 200, 400}
	released := []int64{5_000, 9_000, 7_000, 0}
	ids := make([]int64, len(searched))
	for index := range searched {
		work := createWork(t, opened, "music", "Album "+string(rune('A'+index)))
		item := createDesired(t, opened, work.ID, "SET", 10)
		ids[index] = item.ID
		mustRecordSearched(t, desired, item.ID, searched[index])
	}
	inputs, err := hunt.PlanInputs(t.Context(), true, 0, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	planned := make([]library.PlannedHunt, len(inputs))
	for index, input := range inputs {
		date := released[index]
		planned[index] = library.PlannedHunt{Input: input, Class: "missing", Priority: 1}
		if date != 0 {
			planned[index].ReleaseDate = &date
		}
	}
	if _, err := hunt.ApplyPlan(t.Context(), planned, 1); err != nil {
		t.Fatal(err)
	}
	// ApplyPlan leaves the search record alone; set it again for the order tests.
	for index, id := range ids {
		mustRecordSearched(t, desired, id, searched[index]+1_000)
	}

	draw := func(mode library.HuntMode, from, before int64) []int64 {
		got, err := hunt.NextToHunt(t.Context(), library.HuntSelection{
			Class: "missing", Mode: mode, Now: 1_000_000, Limit: 10, ReleaseFrom: from, ReleaseBefore: before,
		})
		if err != nil {
			t.Fatalf("NextToHunt %s: %v", mode, err)
		}
		return got
	}
	for _, check := range []struct {
		mode library.HuntMode
		want []int64
	}{
		{library.HuntOldestSearched, []int64{ids[1], ids[2], ids[0], ids[3]}},
		{library.HuntNewestSearched, []int64{ids[3], ids[0], ids[2], ids[1]}},
		{library.HuntOldestRelease, []int64{ids[3], ids[0], ids[2], ids[1]}},
		{library.HuntNewestRelease, []int64{ids[1], ids[2], ids[0], ids[3]}},
	} {
		if got := draw(check.mode, 0, 0); !slices.Equal(got, check.want) {
			t.Errorf("%s = %v, want %v", check.mode, got, check.want)
		}
	}
	if got := draw(library.HuntRandomOldestSearched, 0, 0); len(got) != 4 {
		t.Errorf("random_oldest_searched drew %v, want all four", got)
	}
	if got := draw(library.HuntOldestSearched, 6_000, 0); !slices.Equal(got, []int64{ids[1], ids[2]}) {
		t.Errorf("new releases (from 6000) = %v, want the two released after it", got)
	}
	if got := draw(library.HuntOldestSearched, 0, 6_000); !slices.Equal(got, []int64{ids[0], ids[3]}) {
		t.Errorf("gaps (before 6000) = %v, want the older one and the undated one", got)
	}
}
