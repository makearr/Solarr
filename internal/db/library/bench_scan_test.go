package library_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
)

// scanBatchRows matches DefaultBatchSize (internal/scanning/scan) and importBatchRows above:
// one committed batch is 200 files, ADR-3's bulk chunk.
const scanBatchRows = 200

// registerBatchTarget reuses importBatchTarget's number: RegisterBatch does the same
// two inserts per file CreateMediaObject/AddLibraryPath already measure, just behind
// a find-or-create lookup first.
const registerBatchTarget = importBatchTarget

// TestADR28_ScanRegisterBatchStaysUnderItsTarget measures library.Physical against a
// corpus already at ADR-22 scale, so every dedupe lookup (GetPathByLocation,
// FindMediaObjectsByQuickHash) hits a populated index -- the realistic case, not an
// empty table's best case.
func TestADR28_ScanRegisterBatchStaysUnderItsTarget(t *testing.T) {
	timed(t)
	opened := writableCorpus(t)
	physical := library.NewPhysical(opened)
	seenAt := time.Unix(baseTime, 0)

	durations := measure(20, func(round int) {
		files := make([]library.PhysicalFile, scanBatchRows)
		for index := range files {
			serial := 3_000_000 + round*scanBatchRows + index
			files[index] = library.PhysicalFile{
				RelPath:     fmt.Sprintf("bench-scan/%d.flac", serial),
				SizeBytes:   1024,
				QuickHash:   fmt.Sprintf("bench-q%012x", serial),
				FirstSeenAt: seenAt,
			}
		}
		if _, err := physical.RegisterBatch(t.Context(), 1, files); err != nil {
			t.Fatalf("RegisterBatch: %v", err)
		}
	})
	assertTarget(t, "scan register batch (new files)", durations, registerBatchTarget)
}

// TestADR28_ScanRegisterBatchOfKnownPathsStaysUnderItsTarget is the ordinary case on
// every sweep after the first: every path already has a library_path row, so this
// measures the GetPathByLocation short-circuit, not first registration.
func TestADR28_ScanRegisterBatchOfKnownPathsStaysUnderItsTarget(t *testing.T) {
	timed(t)
	opened := writableCorpus(t)
	physical := library.NewPhysical(opened)
	seenAt := time.Unix(baseTime, 0)

	files := make([]library.PhysicalFile, scanBatchRows)
	for index := range files {
		files[index] = library.PhysicalFile{
			RelPath:     fmt.Sprintf("bench-known/%d.flac", index),
			SizeBytes:   1024,
			QuickHash:   fmt.Sprintf("bench-known-q%012x", index),
			FirstSeenAt: seenAt,
		}
	}
	if _, err := physical.RegisterBatch(t.Context(), 1, files); err != nil {
		t.Fatalf("seed RegisterBatch: %v", err)
	}

	durations := measure(20, func(int) {
		if _, err := physical.RegisterBatch(t.Context(), 1, files); err != nil {
			t.Fatalf("RegisterBatch: %v", err)
		}
	})
	assertTarget(t, "scan register batch (known paths)", durations, registerBatchTarget)
}

// TestADR28_ScanResolveWorksStaysUnderItsTarget is library.Identity's half: every
// link names a work that does not exist yet, the more expensive branch (a create plus
// a link, not just the lookup).
func TestADR28_ScanResolveWorksStaysUnderItsTarget(t *testing.T) {
	timed(t)
	opened := writableCorpus(t)
	identity := library.NewIdentity(opened)
	at := time.Unix(baseTime, 0)

	durations := measure(20, func(round int) {
		links := make([]library.WorkLink, scanBatchRows)
		for index := range links {
			serial := 3_000_000 + round*scanBatchRows + index
			external := fmt.Sprintf("bench-rg-%012x", serial)
			links[index] = library.WorkLink{
				Provider: "musicbrainz:release-group", ExternalID: external, Confidence: 1.0,
				Work: library.WorkIdentity{
					MediaType: "music", CanonicalTitle: external, SortTitle: external,
				},
			}
		}
		if _, err := identity.ResolveWorks(t.Context(), at, links); err != nil {
			t.Fatalf("ResolveWorks: %v", err)
		}
	})
	assertTarget(t, "scan resolve works batch (new works)", durations, registerBatchTarget)
}
