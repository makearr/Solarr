package library_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
)

// A stop after paths were marked and before their works were recounted leaves a pending marking, which the next start finishes.
func TestFinishPendingRecounts_BringsItemsRightAfterAStopMidMarking(t *testing.T) {
	opened, queries := newLibrary(t)
	physical := library.NewPhysical(opened)
	files := []library.PhysicalFile{
		{RelPath: "Show/S01E01.mkv", SizeBytes: 1, QuickHash: "one", FirstSeenAt: time.Unix(1_700_000_000, 0).UTC()},
		{RelPath: "Show/S01E02.mkv", SizeBytes: 1, QuickHash: "two", FirstSeenAt: time.Unix(1_700_000_000, 0).UTC()},
	}
	ids, err := physical.RegisterBatch(t.Context(), 1, files)
	if err != nil {
		t.Fatalf("RegisterBatch: %v", err)
	}
	work := createWork(t, opened, "tv", "Invented Show")
	item := createDesired(t, opened, work.ID, "SET", 2)
	attach(t, opened, item.ID, ids[0].MediaObjectID, "primary", "s1:e1")
	attach(t, opened, item.ID, ids[1].MediaObjectID, "primary", "s1:e2")
	if err := physical.RecomputeWorkItems(t.Context(), work.ID); err != nil {
		t.Fatalf("RecomputeWorkItems: %v", err)
	}

	const since = 1_700_100_000
	err = opened.Write(t.Context(), store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO missing_recount (root_folder_id, missing_since) VALUES (1, ?)`, since); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE library_path SET missing_since = ? WHERE rel_path = 'Show/S01E01.mkv'`, since)
		return err
	})
	if err != nil {
		t.Fatalf("simulate the stop: %v", err)
	}

	for call := 1; call <= 2; call++ {
		if err := physical.FinishPendingRecounts(t.Context()); err != nil {
			t.Fatalf("FinishPendingRecounts call %d: %v", call, err)
		}
		got, err := queries.GetDesiredItem(t.Context(), item.ID)
		if err != nil {
			t.Fatalf("GetDesiredItem: %v", err)
		}
		if got.State != "PARTIAL" || got.PresentCount != 1 {
			t.Fatalf("call %d: state %s present %d, want PARTIAL 1", call, got.State, got.PresentCount)
		}
	}
	pending, err := queries.ListMissingRecounts(t.Context())
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending after finishing = %v, %v; want none", pending, err)
	}
}

// A marking whose recount failed (or never ran) stays on record, so the next start recounts it.
func TestMarking_ARecountThatFailsLeavesTheMarkingForTheNextStart(t *testing.T) {
	marks := map[string]struct {
		mark    func(p library.Physical, firstPath int64, at time.Time) error
		present int64
		state   string
	}{
		"by id": {mark: func(p library.Physical, firstPath int64, at time.Time) error {
			_, err := p.MarkPathsMissing(t.Context(), 1, []int64{firstPath}, at.Add(time.Hour))
			return err
		}, present: 1, state: "PARTIAL"},
		"by sweep": {mark: func(p library.Physical, _ int64, at time.Time) error {
			_, err := p.MarkMissing(t.Context(), 1, at.Add(time.Hour))
			return err
		}, present: 0, state: "MISSING"},
	}
	for name, c := range marks {
		t.Run(name, func(t *testing.T) { checkFailedRecountStaysPending(t, c.mark, c.present, c.state) })
	}
}

func checkFailedRecountStaysPending(t *testing.T, mark func(p library.Physical, firstPath int64, at time.Time) error, present int64, state string) {
	t.Helper()
	opened, queries := newLibrary(t)
	physical := library.NewPhysical(opened)
	at := time.Unix(1_700_000_000, 0).UTC()
	ids, err := physical.RegisterBatch(t.Context(), 1, []library.PhysicalFile{
		{RelPath: "Show/S01E01.mkv", SizeBytes: 1, QuickHash: "one", FirstSeenAt: at},
		{RelPath: "Show/S01E02.mkv", SizeBytes: 1, QuickHash: "two", FirstSeenAt: at},
	})
	if err != nil {
		t.Fatalf("RegisterBatch: %v", err)
	}
	work := createWork(t, opened, "tv", "Invented Show")
	item := createDesired(t, opened, work.ID, "SET", 2)
	attach(t, opened, item.ID, ids[0].MediaObjectID, "primary", "s1:e1")
	attach(t, opened, item.ID, ids[1].MediaObjectID, "primary", "s1:e2")
	if err := physical.RecomputeWorkItems(t.Context(), work.ID); err != nil {
		t.Fatalf("RecomputeWorkItems: %v", err)
	}
	if _, err := opened.WriteDB().ExecContext(t.Context(),
		`CREATE TRIGGER refuse_recount BEFORE UPDATE ON desired_item BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	if err := mark(physical, ids[0].LibraryPathID, at); err == nil {
		t.Fatal("marking succeeded although the recount was refused")
	}
	pending, err := queries.ListMissingRecounts(t.Context())
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending after the failed recount = %v, %v; want the one marking", pending, err)
	}
	if _, err := opened.WriteDB().ExecContext(t.Context(), `DROP TRIGGER refuse_recount`); err != nil {
		t.Fatalf("drop trigger: %v", err)
	}
	if err := physical.FinishPendingRecounts(t.Context()); err != nil {
		t.Fatalf("FinishPendingRecounts: %v", err)
	}
	got, err := queries.GetDesiredItem(t.Context(), item.ID)
	if err != nil || got.State != state || got.PresentCount != present {
		t.Fatalf("item = %+v, %v; want %s %d", got, err, state, present)
	}
}
