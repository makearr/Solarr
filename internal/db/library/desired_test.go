package library_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/makearr/MediaNexus/internal/db/library"
	"github.com/makearr/MediaNexus/internal/db/store"
	"github.com/makearr/MediaNexus/internal/db/store/gen/librarydb"
)

func createDesired(t *testing.T, opened *store.Store, workID int64, kind string, required int64) librarydb.DesiredItem {
	t.Helper()
	var item librarydb.DesiredItem
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		row, err := q.CreateDesiredItem(ctx, librarydb.CreateDesiredItemParams{
			WorkID: workID, CategoryID: 1, ProfileID: 1, Monitored: 1,
			// Explicit: an insert overrides the column default, so a new item
			// that is not dirty would never reach the planner.
			Dirty:            1,
			SatisfactionKind: kind,
			RequiredCount:    sql.NullInt64{Int64: required, Valid: required > 0},
			State:            "MISSING",
		})
		item = row
		return err
	})
	return item
}

func attach(t *testing.T, opened *store.Store, itemID, objectID int64, role, mediaRef string) {
	t.Helper()
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.AttachFile(ctx, librarydb.AttachFileParams{
			DesiredItemID: itemID, MediaObjectID: objectID,
			Role: role, MediaRef: mediaRef, AddedAt: 1,
		})
	})
}

// The link a library scan needs. Without it the only path from bytes back to
// intent runs through acquisition, and a scan acquires nothing.
func TestAFileFoundByScanningKnowsWhatItBelongsTo(t *testing.T) {
	opened, queries := newLibrary(t)
	work := createWork(t, opened, "music", "Tender Prey")
	item := createDesired(t, opened, work.ID, "SINGLE", 1)
	object := createObject(t, opened, "quick-1", 4096)

	attach(t, opened, item.ID, object.ID, "primary", "")

	owners, err := queries.ListDesiredItemsForFile(t.Context(), object.ID)
	if err != nil {
		t.Fatalf("ListDesiredItemsForFile: %v", err)
	}
	if len(owners) != 1 || owners[0].DesiredItemID != item.ID {
		t.Errorf("the file resolved to %d owners, want desired item %d", len(owners), item.ID)
	}
}

// Extras belong to the work without satisfying it. A trailer that counted would
// mark a film complete that nobody has.
func TestExtrasDoNotSatisfyTheItem(t *testing.T) {
	opened, queries := newLibrary(t)
	work := createWork(t, opened, "movie", "Cige")
	item := createDesired(t, opened, work.ID, "SINGLE", 1)

	trailer := createObject(t, opened, "quick-trailer", 100)
	featurette := createObject(t, opened, "quick-featurette", 200)
	attach(t, opened, item.ID, trailer.ID, "trailer", "")
	attach(t, opened, item.ID, featurette.ID, "featurette", "")

	covered, err := queries.CountPrimaryFiles(t.Context(), item.ID)
	if err != nil {
		t.Fatalf("CountPrimaryFiles: %v", err)
	}
	if covered != 0 {
		t.Errorf("extras counted %d toward satisfaction, want 0", covered)
	}

	// They are still attached, so the interface can show them.
	files, err := queries.ListFilesForDesiredItem(t.Context(), item.ID)
	if err != nil {
		t.Fatalf("ListFilesForDesiredItem: %v", err)
	}
	if len(files) != 2 {
		t.Errorf("%d files attached, want both extras kept", len(files))
	}
}

// One file may be the feature for one item and an extra for another, so the
// role belongs to the relationship rather than to the bytes.
func TestOneFileCanHoldDifferentRoles(t *testing.T) {
	opened, queries := newLibrary(t)
	work := createWork(t, opened, "movie", "Cige")
	feature := createDesired(t, opened, work.ID, "SINGLE", 1)
	object := createObject(t, opened, "quick-1", 10)

	attach(t, opened, feature.ID, object.ID, "primary", "")
	attach(t, opened, feature.ID, object.ID, "trailer", "")

	files, err := queries.ListFilesForDesiredItem(t.Context(), feature.ID)
	if err != nil {
		t.Fatalf("ListFilesForDesiredItem: %v", err)
	}
	if len(files) != 2 {
		t.Errorf("%d rows, want the same object under two roles", len(files))
	}
}

// A season pack covers many parts at once, so satisfaction counts distinct
// parts rather than files (ADR-23).
func TestSetSatisfactionCountsDistinctParts(t *testing.T) {
	opened, queries := newLibrary(t)
	work := createWork(t, opened, "tv", "Nathan for You")
	season := createDesired(t, opened, work.ID, "SET", 8)

	for episode := 1; episode <= 3; episode++ {
		object := createObject(t, opened, fmt.Sprintf("quick-e%d", episode), int64(episode))
		attach(t, opened, season.ID, object.ID, "primary", fmt.Sprintf("s03e%02d", episode))
	}
	// The same episode again, from a different release.
	duplicate := createObject(t, opened, "quick-e1-again", 99)
	attach(t, opened, season.ID, duplicate.ID, "primary", "s03e01")

	covered, err := queries.CountPrimaryFiles(t.Context(), season.ID)
	if err != nil {
		t.Fatalf("CountPrimaryFiles: %v", err)
	}
	if covered != 3 {
		t.Errorf("covered %d parts, want 3 -- a second copy of one episode is not progress", covered)
	}
}

// Counters drift, so the repair path is not optional (ADR-28).
func TestPresentCountIsRecomputableAfterDrift(t *testing.T) {
	opened, queries := newLibrary(t)
	work := createWork(t, opened, "tv", "Nathan for You")
	season := createDesired(t, opened, work.ID, "SET", 8)

	for episode := 1; episode <= 4; episode++ {
		object := createObject(t, opened, fmt.Sprintf("quick-e%d", episode), int64(episode))
		attach(t, opened, season.ID, object.ID, "primary", fmt.Sprintf("s03e%02d", episode))
	}

	// Drift, as an interrupted import would leave it.
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.SetDesiredItemState(ctx, librarydb.SetDesiredItemStateParams{
			ID: season.ID, State: "PARTIAL", PresentCount: 99,
		})
	})

	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.RecomputePresentCount(ctx, season.ID)
	})

	after, err := queries.GetDesiredItem(t.Context(), season.ID)
	if err != nil {
		t.Fatalf("GetDesiredItem: %v", err)
	}
	if after.PresentCount != 4 {
		t.Errorf("present_count = %d after repair, want 4", after.PresentCount)
	}
}

// A dashboard figure is one lookup whatever the library size (ADR-28).
func TestCounterAdjustsIncrementally(t *testing.T) {
	opened, queries := newLibrary(t)

	for _, delta := range []int64{5, 3, -2} {
		write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
			return q.AdjustCounter(ctx, librarydb.AdjustCounterParams{
				Scope: "category:1", Metric: "items_missing", Value: delta, UpdatedAt: 1,
			})
		})
	}

	counter, err := queries.GetCounter(t.Context(),
		librarydb.GetCounterParams{Scope: "category:1", Metric: "items_missing"})
	if err != nil {
		t.Fatalf("GetCounter: %v", err)
	}
	if counter.Value != 6 {
		t.Errorf("counter = %d, want 6", counter.Value)
	}
}

// The reconciliation job needs a truth to compare a counter against.
func TestCountersCanBeCheckedAgainstTheRealCount(t *testing.T) {
	opened, queries := newLibrary(t)
	work := createWork(t, opened, "tv", "Nathan for You")

	for index := range 3 {
		item := createDesired(t, opened, work.ID, "SINGLE", 1)
		write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
			state := "MISSING"
			if index == 0 {
				state = "COMPLETE"
			}
			return q.SetDesiredItemState(ctx, librarydb.SetDesiredItemStateParams{
				ID: item.ID, State: state, PresentCount: 0,
			})
		})
	}

	totals, err := queries.CountDesiredItemsByState(t.Context(), 0)
	if err != nil {
		t.Fatalf("CountDesiredItemsByState: %v", err)
	}
	byState := map[string]int64{}
	for _, row := range totals {
		byState[row.State] = row.Total
	}
	if byState["MISSING"] != 2 || byState["COMPLETE"] != 1 {
		t.Errorf("states = %v, want 2 missing and 1 complete", byState)
	}
}

// Library health's own "missing tracks" figure (todo 4.8): only monitored
// MISSING/PARTIAL items count, COMPLETE and unmonitored ones do not.
func TestDesired_CountIncompleteAndIncompleteItems(t *testing.T) {
	opened, _ := newLibrary(t)
	work := createWork(t, opened, "music", "Zor A")

	missing := createDesired(t, opened, work.ID, "SET", 10)
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.SetDesiredItemState(ctx, librarydb.SetDesiredItemStateParams{ID: missing.ID, State: "MISSING", PresentCount: 0})
	})
	partial := createDesired(t, opened, work.ID, "SINGLE", 1)
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.SetDesiredItemState(ctx, librarydb.SetDesiredItemStateParams{ID: partial.ID, State: "PARTIAL", PresentCount: 1})
	})
	complete := createDesired(t, opened, work.ID, "SINGLE", 1)
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.SetDesiredItemState(ctx, librarydb.SetDesiredItemStateParams{ID: complete.ID, State: "COMPLETE", PresentCount: 1})
	})
	unmonitored := createDesired(t, opened, work.ID, "SINGLE", 1)
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		if err := q.SetDesiredItemState(ctx, librarydb.SetDesiredItemStateParams{ID: unmonitored.ID, State: "MISSING", PresentCount: 0}); err != nil {
			return err
		}
		return q.SetDesiredItemMonitored(ctx, librarydb.SetDesiredItemMonitoredParams{ID: unmonitored.ID, Monitored: 0})
	})

	desired := library.NewDesired(opened)
	count, err := desired.CountIncomplete(t.Context(), 0)
	if err != nil {
		t.Fatalf("CountIncomplete: %v", err)
	}
	if count != 2 {
		t.Errorf("CountIncomplete = %d, want 2 (missing + partial, not complete or unmonitored)", count)
	}

	items, err := desired.IncompleteItems(t.Context(), 0, 0, 10)
	if err != nil {
		t.Fatalf("IncompleteItems: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("IncompleteItems = %+v, want 2", items)
	}
	states := map[int64]string{items[0].ID: items[0].State, items[1].ID: items[1].State}
	if states[missing.ID] != "MISSING" || states[partial.ID] != "PARTIAL" {
		t.Errorf("states = %+v, want MISSING for %d and PARTIAL for %d", states, missing.ID, partial.ID)
	}
	for _, item := range items {
		if item.Title != "Zor A" {
			t.Errorf("item %+v has the wrong title, want %q", item, "Zor A")
		}
	}
}

// The planner reads a dirty set, not the library (ADR-6), and the generation
// guard stops a stale plan overwriting a newer one.
func TestPlanningClearsOnlyTheGenerationItRead(t *testing.T) {
	opened, queries := newLibrary(t)
	work := createWork(t, opened, "movie", "Cige")
	item := createDesired(t, opened, work.ID, "SINGLE", 1)

	dirty, err := queries.ListDirtyDesiredItems(t.Context(),
		librarydb.ListDirtyDesiredItemsParams{AfterID: 0, PageSize: 10})
	if err != nil {
		t.Fatalf("ListDirtyDesiredItems: %v", err)
	}
	if len(dirty) != 1 {
		t.Fatalf("%d dirty items, want 1 -- a new item plans on its first tick", len(dirty))
	}

	// Something marks it dirty again while the planner is working.
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.MarkDesiredItemDirty(ctx, item.ID)
	})
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.MarkDesiredItemPlanned(ctx, librarydb.MarkDesiredItemPlannedParams{
			ID: item.ID, LastPlannedAt: sql.NullInt64{Int64: 5, Valid: true}, Generation: 0,
		})
	})

	after, err := queries.GetDesiredItem(t.Context(), item.ID)
	if err != nil {
		t.Fatalf("GetDesiredItem: %v", err)
	}
	if after.Generation != 1 {
		t.Errorf("generation = %d, want 1", after.Generation)
	}
}

// Hunting is ordered by the mode's covering index, never by scanning the library.
func TestHuntQueueOrdersByPriorityThenOldestSearched(t *testing.T) {
	opened, queries := newLibrary(t)
	work := createWork(t, opened, "movie", "Cige")

	for _, entry := range []struct {
		priority int64
		searched int64
	}{{10, 500}, {90, 900}, {90, 100}} {
		item := createDesired(t, opened, work.ID, "SINGLE", 1)
		write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
			return q.UpsertHuntQueueEntry(ctx, librarydb.UpsertHuntQueueEntryParams{
				DesiredItemID: item.ID, Class: "missing", Priority: entry.priority,
				LastSearchedAt: entry.searched, EligibleAfter: 0,
			})
		})
	}

	next, err := queries.NextHuntByOldestSearched(t.Context(),
		librarydb.NextHuntByOldestSearchedParams{Class: "missing", Now: 1000, PageSize: 10})
	if err != nil {
		t.Fatalf("NextHuntByOldestSearched: %v", err)
	}
	if len(next) != 3 {
		t.Fatalf("%d entries, want 3", len(next))
	}
	// Highest priority first, and within it the one waiting longest.
	if next[0].Priority != 90 || next[0].LastSearchedAt != 100 {
		t.Errorf("first entry is priority %d searched at %d, want 90 at 100",
			next[0].Priority, next[0].LastSearchedAt)
	}
}

// An item not yet available must not be hunted for (ADR-23).
func TestHuntQueueSkipsWhatIsNotYetEligible(t *testing.T) {
	opened, queries := newLibrary(t)
	work := createWork(t, opened, "movie", "Cige")
	item := createDesired(t, opened, work.ID, "SINGLE", 1)

	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.UpsertHuntQueueEntry(ctx, librarydb.UpsertHuntQueueEntryParams{
			DesiredItemID: item.ID, Class: "missing", Priority: 50,
			EligibleAfter: 5_000,
		})
	})

	next, err := queries.NextHuntByOldestSearched(t.Context(),
		librarydb.NextHuntByOldestSearchedParams{Class: "missing", Now: 1_000, PageSize: 10})
	if err != nil {
		t.Fatalf("NextHuntByOldestSearched: %v", err)
	}
	if len(next) != 0 {
		t.Errorf("%d entries returned before they were eligible", len(next))
	}
}

// IdentityForFile is what a rename preview needs: which work and which
// media_ref a file's primary coverage names, with no edition (an "any
// pressing" SET) reported as a nil EditionID rather than a fabricated one.
func TestIdentityForFile_ResolvesWorkAndMediaRefFromPrimaryCoverage(t *testing.T) {
	opened, _ := newLibrary(t)
	work := createWork(t, opened, "music", "Tender Prey")
	item := createDesired(t, opened, work.ID, "SET", 3)
	object := createObject(t, opened, "quick-1", 4096)
	attach(t, opened, item.ID, object.ID, "primary", "disc1:track3")

	desired := library.NewDesired(opened)
	identity, found, err := desired.IdentityForFile(t.Context(), object.ID)
	if err != nil {
		t.Fatalf("IdentityForFile: %v", err)
	}
	if !found {
		t.Fatal("IdentityForFile: found = false, want true")
	}
	if identity.WorkID != work.ID || identity.MediaRef != "disc1:track3" || identity.EditionID != nil {
		t.Errorf("identity = %+v, want work %d, media_ref disc1:track3, no edition", identity, work.ID)
	}
}

// A non-primary role (a trailer, a bonus file) does not name the file for
// renaming purposes -- the same reasoning CountPrimaryFiles already applies
// to satisfaction.
func TestIdentityForFile_IgnoresNonPrimaryCoverage(t *testing.T) {
	opened, _ := newLibrary(t)
	work := createWork(t, opened, "music", "Tender Prey")
	item := createDesired(t, opened, work.ID, "SET", 3)
	object := createObject(t, opened, "quick-bonus", 4096)
	attach(t, opened, item.ID, object.ID, "bonus", "disc1:track99")

	desired := library.NewDesired(opened)
	_, found, err := desired.IdentityForFile(t.Context(), object.ID)
	if err != nil {
		t.Fatalf("IdentityForFile: %v", err)
	}
	if found {
		t.Error("IdentityForFile found a bonus-role file, want it ignored")
	}
}

// A file with no desired_item_file row at all -- never aligned -- has no
// identity to name it by.
func TestIdentityForFile_NotFoundWhenNeverAligned(t *testing.T) {
	opened, _ := newLibrary(t)
	object := createObject(t, opened, "quick-orphan", 4096)

	desired := library.NewDesired(opened)
	_, found, err := desired.IdentityForFile(t.Context(), object.ID)
	if err != nil {
		t.Fatalf("IdentityForFile: %v", err)
	}
	if found {
		t.Error("IdentityForFile found an identity for an unaligned file, want none")
	}
}

// Removing an item takes its queue entry and its file links with it.
func TestDeletingADesiredItemCascades(t *testing.T) {
	opened, queries := newLibrary(t)
	work := createWork(t, opened, "movie", "Cige")
	item := createDesired(t, opened, work.ID, "SINGLE", 1)
	object := createObject(t, opened, "quick-1", 10)
	attach(t, opened, item.ID, object.ID, "primary", "")

	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.DeleteWork(ctx, work.ID)
	})

	owners, err := queries.ListDesiredItemsForFile(t.Context(), object.ID)
	if err != nil {
		t.Fatalf("ListDesiredItemsForFile: %v", err)
	}
	if len(owners) != 0 {
		t.Errorf("%d links survived the work", len(owners))
	}
}
