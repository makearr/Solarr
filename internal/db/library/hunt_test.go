package library_test

import (
	"context"
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

	got, err := hunt.PlanInputs(t.Context(), true, 0, 10)
	if err != nil {
		t.Fatalf("PlanInputs: %v", err)
	}
	want := []library.PlanInput{{
		ID: item.ID, CategoryID: 1, Monitored: true, SatisfactionKind: "SET", State: "MISSING",
		ReleaseDate: "1988-05-02", LastSearchedAt: 4_000, SearchCount: 1,
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
	inputs, err := hunt.PlanInputs(t.Context(), true, 0, 10)
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
	if dirty, _ := hunt.PlanInputs(t.Context(), true, 0, 10); len(dirty) != 0 {
		t.Fatalf("%d items still dirty after planning", len(dirty))
	}
}

// A state change between the read and the write means the plan is stale: it is not written.
func TestApplyPlanSkipsAnItemChangedSinceItWasRead(t *testing.T) {
	opened, queries := newLibrary(t)
	work := createWork(t, opened, "music", "Tender Prey")
	item := createDesired(t, opened, work.ID, "SET", 12)
	hunt := library.NewHunt(opened)
	inputs, err := hunt.PlanInputs(t.Context(), true, 0, 10)
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
	if next, _ := hunt.NextToHunt(t.Context(), 5_000, 10); len(next) != 0 {
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
	inputs, _ := hunt.PlanInputs(t.Context(), true, 0, 10)
	if _, err := hunt.ApplyPlan(t.Context(), []library.PlannedHunt{{Input: inputs[0], Class: "missing"}}, 1); err != nil {
		t.Fatalf("ApplyPlan: %v", err)
	}
	if err := hunt.Postpone(t.Context(), item.ID, 9_000); err != nil {
		t.Fatalf("Postpone: %v", err)
	}
	for now, want := range map[int64]int{8_999: 0, 9_000: 1} {
		next, err := hunt.NextToHunt(t.Context(), now, 10)
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
	if err := people.AddCredit(t.Context(), work.ID, artist, "artist", 0); err != nil {
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
