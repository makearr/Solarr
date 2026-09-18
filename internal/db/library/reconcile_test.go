package library_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// createDesiredInCategory is createDesired with an explicit category, for testing
// that reconciliation scopes to one category -- createDesired itself always uses
// category 1, which every other test in this package already depends on.
func createDesiredInCategory(t *testing.T, opened *store.Store, workID, categoryID int64) librarydb.DesiredItem {
	t.Helper()
	var item librarydb.DesiredItem
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		row, err := q.CreateDesiredItem(ctx, librarydb.CreateDesiredItemParams{
			WorkID: workID, CategoryID: categoryID, ProfileID: 1, Monitored: 1,
			Dirty:            1,
			SatisfactionKind: "SINGLE",
			RequiredCount:    sql.NullInt64{Int64: 1, Valid: true},
			State:            "MISSING",
		})
		item = row
		return err
	})
	return item
}

func setState(t *testing.T, opened *store.Store, itemID int64, state string) {
	t.Helper()
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.SetDesiredItemState(ctx, librarydb.SetDesiredItemStateParams{
			State: state, PresentCount: 0, ID: itemID,
		})
	})
}

func setCounter(t *testing.T, opened *store.Store, scope, metric string, value int64) {
	t.Helper()
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.SetCounter(ctx, librarydb.SetCounterParams{
			Scope: scope, Metric: metric, Value: value, UpdatedAt: 1,
		})
	})
}

func correctionFor(corrections []library.Correction, metric string) (library.Correction, bool) {
	for _, c := range corrections {
		if c.Metric == metric {
			return c, true
		}
	}
	return library.Correction{}, false
}

// With no counter ever written, reconciliation must still establish the truth --
// "not yet counted" is not an excuse to leave a dashboard figure wrong.
func TestReconciler_EstablishesCountersThatWereNeverSet(t *testing.T) {
	opened, _ := newLibrary(t)
	work := createWork(t, opened, "tv", "Nathan for You")
	first := createDesired(t, opened, work.ID, "SINGLE", 1)
	second := createDesired(t, opened, work.ID, "SINGLE", 1)
	setState(t, opened, second.ID, "COMPLETE")
	_ = first

	reconciler := library.NewReconciler(opened, clock.NewFakeAtEpoch())
	corrections, err := reconciler.Reconcile(t.Context(), []library.ReconcileScope{{Scope: "library"}})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	missing, ok := correctionFor(corrections, "missing")
	if !ok {
		t.Fatal("no correction reported for missing")
	}
	if missing.WasSet {
		t.Errorf("missing.WasSet = true, want false: it had never been counted")
	}
	if missing.Now != 1 {
		t.Errorf("missing.Now = %d, want 1", missing.Now)
	}

	complete, ok := correctionFor(corrections, "complete")
	if !ok || complete.Now != 1 {
		t.Errorf("complete correction = %+v, ok=%v, want Now=1", complete, ok)
	}

	monitored, ok := correctionFor(corrections, "monitored")
	if !ok || monitored.Now != 2 {
		t.Errorf("monitored correction = %+v, ok=%v, want Now=2", monitored, ok)
	}

	counters, err := library.NewCounters(opened).Counters(t.Context(), "library")
	if err != nil {
		t.Fatalf("Counters: %v", err)
	}
	if counters["missing"] != 1 || counters["complete"] != 1 || counters["monitored"] != 2 {
		t.Errorf("counters = %+v, want missing=1 complete=1 monitored=2", counters)
	}
}

// The actual drift scenario Reconcile exists for: a counter left behind by a lost
// write must be corrected to match the library's own truth.
func TestReconciler_CorrectsADriftedCounter(t *testing.T) {
	opened, _ := newLibrary(t)
	work := createWork(t, opened, "movie", "Cige")
	createDesired(t, opened, work.ID, "SINGLE", 1)

	setCounter(t, opened, "library", "missing", 99)

	reconciler := library.NewReconciler(opened, clock.NewFakeAtEpoch())
	corrections, err := reconciler.Reconcile(t.Context(), []library.ReconcileScope{{Scope: "library"}})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	missing, ok := correctionFor(corrections, "missing")
	if !ok {
		t.Fatal("no correction reported for the drifted counter")
	}
	if !missing.WasSet || missing.Was != 99 || missing.Now != 1 {
		t.Errorf("correction = %+v, want Was=99 (WasSet) Now=1", missing)
	}
}

// A counter that already matches the truth must be left alone -- reconciliation is
// a repair path, not something that rewrites every counter on every run.
func TestReconciler_LeavesAnAlreadyCorrectCounterUntouched(t *testing.T) {
	opened, _ := newLibrary(t)
	work := createWork(t, opened, "movie", "Cige")
	createDesired(t, opened, work.ID, "SINGLE", 1)

	setCounter(t, opened, "library", "missing", 1)
	setCounter(t, opened, "library", "partial", 0)
	setCounter(t, opened, "library", "complete", 0)
	setCounter(t, opened, "library", "upgradable", 0)
	setCounter(t, opened, "library", "monitored", 1)

	reconciler := library.NewReconciler(opened, clock.NewFakeAtEpoch())
	corrections, err := reconciler.Reconcile(t.Context(), []library.ReconcileScope{{Scope: "library"}})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(corrections) != 0 {
		t.Errorf("got %d corrections, want 0: every counter already matched truth: %+v",
			len(corrections), corrections)
	}
}

// library.db has no concept of a category (that is settings.db policy, ADR-25), so
// a caller reconciling one category's counters must get only that category's truth.
func TestReconciler_ScopesToOneCategory(t *testing.T) {
	opened, _ := newLibrary(t)
	work := createWork(t, opened, "movie", "Cige")
	inCategory := createDesired(t, opened, work.ID, "SINGLE", 1)
	_ = inCategory // createDesired always uses category 1; see below for category 2

	otherWork := createWork(t, opened, "tv", "Nathan for You")
	other := createDesiredInCategory(t, opened, otherWork.ID, 2)
	_ = other

	reconciler := library.NewReconciler(opened, clock.NewFakeAtEpoch())
	corrections, err := reconciler.Reconcile(t.Context(), []library.ReconcileScope{
		{Scope: "category:1", CategoryID: 1},
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	missing, ok := correctionFor(corrections, "missing")
	if !ok || missing.Now != 1 {
		t.Errorf("category:1 missing = %+v, ok=%v, want Now=1: the other category's item must not count",
			missing, ok)
	}
}
