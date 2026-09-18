package library_test

import (
	"context"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/platform/clock"
)

func TestProbeCoverage_ReconcileCountsProbedAgainstTotal(t *testing.T) {
	opened, _ := newLibrary(t)
	physical := library.NewPhysical(opened)
	coverage := library.NewProbeCoverage(opened, clock.NewFakeAtEpoch())

	measured := createObject(t, opened, "probed-hash", 100)
	unmeasured := createObject(t, opened, "unprobed-hash", 200)
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		for _, object := range []struct {
			id      int64
			relPath string
		}{{measured.ID, "a.flac"}, {unmeasured.ID, "b.flac"}} {
			if _, err := q.AddLibraryPath(ctx, librarydb.AddLibraryPathParams{
				MediaObjectID: object.id, RootFolderID: 1, RelPath: object.relPath,
				LinkKind: "primary", IsPrimary: 1, CreatedAt: 1,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err := physical.RecordMeasured(t.Context(), measured.ID,
		library.AttributeClaim{AudioCodec: "flac"}, library.MeasuredScalars{}, "", 1); err != nil {
		t.Fatalf("RecordMeasured: %v", err)
	}

	corrections, err := coverage.Reconcile(t.Context())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(corrections) != 2 {
		t.Fatalf("Reconcile made %d corrections, want 2 (probed, total_files) on first run", len(corrections))
	}

	byMetric := make(map[string]int64, len(corrections))
	for _, c := range corrections {
		byMetric[c.Metric] = c.Now
	}
	if byMetric["probed"] != 1 {
		t.Errorf("probed = %d, want 1", byMetric["probed"])
	}
	if byMetric["total_files"] != 2 {
		t.Errorf("total_files = %d, want 2", byMetric["total_files"])
	}

	// A second reconcile with nothing changed must not report the same
	// counters as corrections again -- drift-and-repair only reports drift.
	again, err := coverage.Reconcile(t.Context())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("second Reconcile made %d corrections, want 0 when nothing changed", len(again))
	}
}
