package library_test

import (
	"testing"

	"github.com/makearr/MediaNexus/internal/db/library"
	"github.com/makearr/MediaNexus/internal/platform/clock"
)

func TestFreshCounts_ReconcileCountsWorksOnOrAfterTheCutoff(t *testing.T) {
	opened, _ := newLibrary(t)
	counts := library.NewFreshCounts(opened, clock.NewFakeAtEpoch())

	createWorkWithDate(t, opened, "Recent Release", "2026-09-05")
	createWorkWithDate(t, opened, "Old Release", "1997-05-21")
	createWorkWithDate(t, opened, "Year Only Release", "2026")
	createWork(t, opened, "music", "No Date At All")

	corrections, err := counts.Reconcile(t.Context(), "2026-09-01")
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(corrections) != 1 || corrections[0].Metric != "fresh" || corrections[0].Now != 1 {
		t.Fatalf("corrections = %+v, want one fresh=1 correction on first run", corrections)
	}

	// A second reconcile with nothing changed must not report the same
	// counter as a correction again -- drift-and-repair only reports drift.
	again, err := counts.Reconcile(t.Context(), "2026-09-01")
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("second Reconcile made %d corrections, want 0 when nothing changed", len(again))
	}
}
