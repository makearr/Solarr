package library_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/makearr/MediaNexus/internal/db/library"
)

// last_seen_at is what a rescan touches, whether the path is brand new or already
// known -- otherwise a re-observed file would look no different from one nobody has
// looked at since the epoch, and MarkMissing could not tell them apart.
func TestRegisterBatch_TouchesLastSeenAt(t *testing.T) {
	opened, queries := newLibrary(t)
	physical := library.NewPhysical(opened)

	firstSeenAt := time.Unix(1_700_000_000, 0).UTC()
	file := library.PhysicalFile{
		RelPath: "Artist/Album/01.flac", SizeBytes: 1024, QuickHash: "hash-a", FirstSeenAt: firstSeenAt,
	}
	ids, err := physical.RegisterBatch(t.Context(), 1, []library.PhysicalFile{file})
	if err != nil {
		t.Fatalf("RegisterBatch (first): %v", err)
	}

	rows, err := queries.ListPathsForObject(t.Context(), ids[0].MediaObjectID)
	if err != nil {
		t.Fatalf("ListPathsForObject: %v", err)
	}
	if len(rows) != 1 || rows[0].LastSeenAt != firstSeenAt.Unix() {
		t.Fatalf("rows = %+v, want one row with last_seen_at %d", rows, firstSeenAt.Unix())
	}

	rescanAt := firstSeenAt.Add(24 * time.Hour)
	file.FirstSeenAt = rescanAt
	if _, err := physical.RegisterBatch(t.Context(), 1, []library.PhysicalFile{file}); err != nil {
		t.Fatalf("RegisterBatch (second): %v", err)
	}

	rows, err = queries.ListPathsForObject(t.Context(), ids[0].MediaObjectID)
	if err != nil {
		t.Fatalf("ListPathsForObject: %v", err)
	}
	if len(rows) != 1 || rows[0].LastSeenAt != rescanAt.Unix() {
		t.Fatalf("rows = %+v, want last_seen_at updated to %d", rows, rescanAt.Unix())
	}
}

// The core of section 10.4: a path not seen since before the cutoff gets marked, one
// seen at or after it is left alone.
func TestMarkMissing_MarksOnlyStalePaths(t *testing.T) {
	opened, queries := newLibrary(t)
	physical := library.NewPhysical(opened)

	cutoff := time.Unix(1_700_100_000, 0).UTC()
	stale := library.PhysicalFile{
		RelPath: "stale.flac", SizeBytes: 1, QuickHash: "stale",
		FirstSeenAt: cutoff.Add(-time.Hour),
	}
	fresh := library.PhysicalFile{
		RelPath: "fresh.flac", SizeBytes: 1, QuickHash: "fresh",
		FirstSeenAt: cutoff.Add(time.Hour),
	}
	ids, err := physical.RegisterBatch(t.Context(), 1, []library.PhysicalFile{stale, fresh})
	if err != nil {
		t.Fatalf("RegisterBatch: %v", err)
	}

	marked, err := physical.MarkMissing(t.Context(), 1, cutoff)
	if err != nil {
		t.Fatalf("MarkMissing: %v", err)
	}
	if marked != 1 {
		t.Errorf("marked %d, want 1", marked)
	}

	staleRow, err := queries.ListPathsForObject(t.Context(), ids[0].MediaObjectID)
	if err != nil {
		t.Fatalf("ListPathsForObject(stale): %v", err)
	}
	if len(staleRow) != 1 || !staleRow[0].MissingSince.Valid {
		t.Errorf("stale row = %+v, want missing_since set", staleRow)
	}

	freshRow, err := queries.ListPathsForObject(t.Context(), ids[1].MediaObjectID)
	if err != nil {
		t.Fatalf("ListPathsForObject(fresh): %v", err)
	}
	if len(freshRow) != 1 || freshRow[0].MissingSince.Valid {
		t.Errorf("fresh row = %+v, want missing_since still NULL", freshRow)
	}
}

// A root's paths outnumbering the 200-row batch is exactly the off-by-one this
// codebase has been bitten by before (CLAUDE.md): every stale row must be marked,
// not just the first page.
func TestMarkMissing_CoversMultipleBatches(t *testing.T) {
	opened, _ := newLibrary(t)
	physical := library.NewPhysical(opened)

	cutoff := time.Unix(1_700_100_000, 0).UTC()
	const total = 450
	files := make([]library.PhysicalFile, total)
	for index := range total {
		files[index] = library.PhysicalFile{
			RelPath: fmt.Sprintf("track-%03d.flac", index), SizeBytes: 1,
			QuickHash: fmt.Sprintf("hash-%03d", index), FirstSeenAt: cutoff.Add(-time.Hour),
		}
	}
	if _, err := physical.RegisterBatch(t.Context(), 1, files); err != nil {
		t.Fatalf("RegisterBatch: %v", err)
	}

	marked, err := physical.MarkMissing(t.Context(), 1, cutoff)
	if err != nil {
		t.Fatalf("MarkMissing: %v", err)
	}
	if marked != total {
		t.Errorf("marked %d, want %d", marked, total)
	}

	// Idempotent: a second pass over the same cutoff has nothing left to mark.
	again, err := physical.MarkMissing(t.Context(), 1, cutoff)
	if err != nil {
		t.Fatalf("MarkMissing (second): %v", err)
	}
	if again != 0 {
		t.Errorf("second pass marked %d more, want 0", again)
	}
}

// A file coming back is exactly the case section 10.4 exists to get right: it must
// not stay flagged missing once a scan has seen it again.
func TestRegisterBatch_ReobservingAMissingPathClearsMissingSince(t *testing.T) {
	opened, queries := newLibrary(t)
	physical := library.NewPhysical(opened)

	cutoff := time.Unix(1_700_100_000, 0).UTC()
	file := library.PhysicalFile{
		RelPath: "vanishes.flac", SizeBytes: 1, QuickHash: "vanishes",
		FirstSeenAt: cutoff.Add(-time.Hour),
	}
	ids, err := physical.RegisterBatch(t.Context(), 1, []library.PhysicalFile{file})
	if err != nil {
		t.Fatalf("RegisterBatch (first): %v", err)
	}
	if _, err := physical.MarkMissing(t.Context(), 1, cutoff); err != nil {
		t.Fatalf("MarkMissing: %v", err)
	}

	rows, err := queries.ListPathsForObject(t.Context(), ids[0].MediaObjectID)
	if err != nil {
		t.Fatalf("ListPathsForObject: %v", err)
	}
	if !rows[0].MissingSince.Valid {
		t.Fatalf("row not marked missing before reobservation: %+v", rows[0])
	}

	file.FirstSeenAt = cutoff.Add(time.Hour)
	if _, err := physical.RegisterBatch(t.Context(), 1, []library.PhysicalFile{file}); err != nil {
		t.Fatalf("RegisterBatch (second): %v", err)
	}

	rows, err = queries.ListPathsForObject(t.Context(), ids[0].MediaObjectID)
	if err != nil {
		t.Fatalf("ListPathsForObject: %v", err)
	}
	if rows[0].MissingSince.Valid {
		t.Errorf("row = %+v, want missing_since cleared after reobservation", rows[0])
	}
}
