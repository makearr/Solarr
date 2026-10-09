package library_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/platform/clock"
)

func TestFreshCounts_ReconcileCountsWorksOnOrAfterTheCutoff(t *testing.T) {
	opened, _ := newLibrary(t)
	counts := library.NewFreshCounts(opened, clock.NewFakeAtEpoch())

	createWorkWithDate(t, opened, "Recent Release", "2026-09-05")
	createWorkWithDate(t, opened, "Old Release", "1997-05-21")
	createWorkWithDate(t, opened, "Year Only Release", "2026")
	createWork(t, opened, "music", "No Date At All")

	corrections, err := counts.Reconcile(t.Context(), "2026-09-01", 1_000)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(corrections) != 1 || corrections[0].Metric != "fresh" || corrections[0].Now != 1 {
		t.Fatalf("corrections = %+v, want one fresh=1 correction on first run", corrections)
	}

	// A second reconcile with nothing changed must not report the same
	// counter as a correction again -- drift-and-repair only reports drift.
	again, err := counts.Reconcile(t.Context(), "2026-09-01", 1_000)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("second Reconcile made %d corrections, want 0 when nothing changed", len(again))
	}
}

// ADR-55's second window: an old album an RSS feed showed a release for lately is fresh, one seen long ago is not.
func TestFreshCounts_ARecentRSSSightingMakesAnOldWorkFresh(t *testing.T) {
	opened, _ := newLibrary(t)
	counts := library.NewFreshCounts(opened, clock.NewFakeAtEpoch())
	viaRSS := createDesired(t, opened, createWorkWithDate(t, opened, "Old via RSS", "1997-05-21").ID, "SET", 10)
	viaSearch := createDesired(t, opened, createWorkWithDate(t, opened, "Old via search", "1997-05-22").ID, "SET", 10)
	sightings := library.NewSightings(opened)
	for _, sighting := range []struct{ item, seen int64 }{{viaRSS.ID, 2_000}, {viaSearch.ID, 10}} {
		if err := sightings.RecordRSS(t.Context(), sighting.item, sighting.seen); err != nil {
			t.Fatal(err)
		}
	}

	corrections, err := counts.Reconcile(t.Context(), "2026-09-01", 1_000)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(corrections) != 1 || corrections[0].Now != 1 {
		t.Fatalf("corrections = %+v, want fresh=1: only the work with a recent RSS hit", corrections)
	}
}
