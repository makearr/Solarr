package library_test

import (
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
)

// A file a full sweep found gone stops counting towards its item, and counts again the moment a sweep sees it back.
func TestMarkMissing_AGoneFileStopsCountingAndCountsAgainWhenBack(t *testing.T) {
	opened, queries := newLibrary(t)
	physical := library.NewPhysical(opened)
	cutoff := time.Unix(1_700_100_000, 0).UTC()
	gone := library.PhysicalFile{RelPath: "Show/S01E01.mkv", SizeBytes: 1, QuickHash: "one", FirstSeenAt: cutoff.Add(-time.Hour)}
	here := library.PhysicalFile{RelPath: "Show/S01E02.mkv", SizeBytes: 1, QuickHash: "two", FirstSeenAt: cutoff.Add(time.Hour)}
	ids, err := physical.RegisterBatch(t.Context(), 1, []library.PhysicalFile{gone, here})
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
	assertItem := func(stage, wantState string, wantPresent int64) {
		t.Helper()
		got, err := queries.GetDesiredItem(t.Context(), item.ID)
		if err != nil {
			t.Fatalf("GetDesiredItem: %v", err)
		}
		if got.State != wantState || got.PresentCount != wantPresent {
			t.Errorf("%s: state %s present %d, want %s %d", stage, got.State, got.PresentCount, wantState, wantPresent)
		}
	}
	assertItem("before", "COMPLETE", 2)

	if _, err := physical.MarkMissing(t.Context(), 1, cutoff); err != nil {
		t.Fatalf("MarkMissing: %v", err)
	}
	assertItem("gone", "PARTIAL", 1)

	gone.FirstSeenAt = cutoff.Add(2 * time.Hour)
	if _, err := physical.RegisterBatch(t.Context(), 1, []library.PhysicalFile{gone}); err != nil {
		t.Fatalf("RegisterBatch (back): %v", err)
	}
	assertItem("back", "COMPLETE", 2)
}

// One path of a file gone while another still holds the bytes is not a file gone.
func TestMarkMissing_AFileWithAnotherPathStillCounts(t *testing.T) {
	opened, queries := newLibrary(t)
	physical := library.NewPhysical(opened)
	cutoff := time.Unix(1_700_100_000, 0).UTC()
	files := []library.PhysicalFile{
		{RelPath: "A/01.flac", SizeBytes: 7, QuickHash: "same", FirstSeenAt: cutoff.Add(-time.Hour)},
		{RelPath: "B/01.flac", SizeBytes: 7, QuickHash: "same", FirstSeenAt: cutoff.Add(time.Hour)},
	}
	ids, err := physical.RegisterBatch(t.Context(), 1, files)
	if err != nil {
		t.Fatalf("RegisterBatch: %v", err)
	}
	if ids[0].MediaObjectID != ids[1].MediaObjectID {
		t.Fatalf("two paths of the same bytes became objects %d and %d", ids[0].MediaObjectID, ids[1].MediaObjectID)
	}
	work := createWork(t, opened, "music", "Invented Album")
	item := createDesired(t, opened, work.ID, "SET", 1)
	attach(t, opened, item.ID, ids[0].MediaObjectID, "primary", "disc1:track1")
	if _, err := physical.MarkMissing(t.Context(), 1, cutoff); err != nil {
		t.Fatalf("MarkMissing: %v", err)
	}
	got, err := queries.GetDesiredItem(t.Context(), item.ID)
	if err != nil {
		t.Fatalf("GetDesiredItem: %v", err)
	}
	if got.State != "COMPLETE" || got.PresentCount != 1 {
		t.Errorf("state %s present %d, want COMPLETE 1", got.State, got.PresentCount)
	}
}

// The guard's two numbers: what one sweep would mark, out of everything the root holds.
func TestVanishing_CountsWhatASweepWouldMark(t *testing.T) {
	opened, _ := newLibrary(t)
	physical := library.NewPhysical(opened)
	cutoff := time.Unix(1_700_100_000, 0).UTC()
	files := []library.PhysicalFile{
		{RelPath: "a.flac", SizeBytes: 1, QuickHash: "a", FirstSeenAt: cutoff.Add(-time.Hour)},
		{RelPath: "b.flac", SizeBytes: 1, QuickHash: "b", FirstSeenAt: cutoff.Add(-time.Hour)},
		{RelPath: "c.flac", SizeBytes: 1, QuickHash: "c", FirstSeenAt: cutoff.Add(time.Hour)},
	}
	if _, err := physical.RegisterBatch(t.Context(), 1, files); err != nil {
		t.Fatalf("RegisterBatch: %v", err)
	}
	if _, err := physical.RegisterBatch(t.Context(), 2, files[:1]); err != nil {
		t.Fatalf("RegisterBatch (other root): %v", err)
	}
	vanishing, total, err := physical.Vanishing(t.Context(), 1, cutoff)
	if err != nil || vanishing != 2 || total != 3 {
		t.Errorf("Vanishing = %d of %d, %v; want 2 of 3", vanishing, total, err)
	}
}
