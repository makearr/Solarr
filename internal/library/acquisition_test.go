package library_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/internal/store/gen/librarydb"
)

func createAcquisition(t *testing.T, opened *store.Store, itemID int64, ref, state string) librarydb.Acquisition {
	t.Helper()
	var acquisition librarydb.Acquisition
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		row, err := q.CreateAcquisition(ctx, librarydb.CreateAcquisitionParams{
			DesiredItemID:     sql.NullInt64{Int64: itemID, Valid: itemID > 0},
			DownloaderID:      1,
			DownloadClientRef: ref,
			Protocol:          "usenet",
			State:             state,
			CreatedAt:         1, UpdatedAt: 1,
		})
		acquisition = row
		return err
	})
	return acquisition
}

// Invariant 15. Without it a retry while the first download is still running
// downloads the same thing twice and races itself on import.
func TestOnlyOneAcquisitionIsInFlightPerItem(t *testing.T) {
	opened, _ := newLibrary(t)
	work := createWork(t, opened, "movie", "Cige")
	item := createDesired(t, opened, work.ID, "SINGLE", 1)

	createAcquisition(t, opened, item.ID, "nzo-1", "DOWNLOADING")

	err := opened.Write(t.Context(), store.PrioInteractive,
		func(ctx context.Context, tx *sql.Tx) error {
			q := librarydb.New(opened.Read()).WithTx(tx)
			_, err := q.CreateAcquisition(ctx, librarydb.CreateAcquisitionParams{
				DesiredItemID: sql.NullInt64{Int64: item.ID, Valid: true},
				DownloaderID:  1, DownloadClientRef: "nzo-2",
				Protocol: "usenet", State: "DOWNLOADING", CreatedAt: 1, UpdatedAt: 1,
			})
			return err
		})
	if err == nil {
		t.Error("a second acquisition started while the first was still in flight")
	}
}

// A finished acquisition frees the item, or nothing could ever be upgraded.
func TestATerminalAcquisitionReleasesTheItem(t *testing.T) {
	opened, _ := newLibrary(t)
	work := createWork(t, opened, "movie", "Cige")
	item := createDesired(t, opened, work.ID, "SINGLE", 1)

	first := createAcquisition(t, opened, item.ID, "nzo-1", "DOWNLOADING")
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.SetAcquisitionState(ctx, librarydb.SetAcquisitionStateParams{
			ID: first.ID, State: "IMPORTED", UpdatedAt: 2,
		})
	})

	second := createAcquisition(t, opened, item.ID, "nzo-2", "DOWNLOADING")
	if second.ID == 0 {
		t.Error("an upgrade could not start after the first acquisition finished")
	}
}

// Section 10.11: the guid identifies a listing on one indexer, the content hash
// identifies the payload wherever it appears. Blocking by guid alone means the
// same dead torrent is re-grabbed from four other indexers carrying it.
func TestBlocklistMatchesByGuidOrByContent(t *testing.T) {
	opened, queries := newLibrary(t)

	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		_, err := q.BlocklistRelease(ctx, librarydb.BlocklistReleaseParams{
			IndexerID:   sql.NullInt64{Int64: 1, Valid: true},
			Guid:        sql.NullString{String: "guid-a", Valid: true},
			ContentHash: sql.NullString{String: "infohash-x", Valid: true},
			Title:       "Cige.2021.2160p", Reason: "CORRUPT", Source: "auto",
			CreatedAt: 1,
		})
		return err
	})

	// Same listing, same indexer.
	byGUID, err := queries.IsBlocklisted(t.Context(), librarydb.IsBlocklistedParams{
		Now: sql.NullInt64{Int64: 10, Valid: true}, IndexerID: sql.NullInt64{Int64: 1, Valid: true},
		Guid: sql.NullString{String: "guid-a", Valid: true},
	})
	if err != nil {
		t.Fatalf("IsBlocklisted: %v", err)
	}
	if byGUID == 0 {
		t.Error("the blocked listing was offered again")
	}

	// Different indexer, different guid, same payload.
	byHash, err := queries.IsBlocklisted(t.Context(), librarydb.IsBlocklistedParams{
		Now: sql.NullInt64{Int64: 10, Valid: true}, IndexerID: sql.NullInt64{Int64: 7, Valid: true},
		Guid:        sql.NullString{String: "guid-elsewhere", Valid: true},
		ContentHash: sql.NullString{String: "infohash-x", Valid: true},
	})
	if err != nil {
		t.Fatalf("IsBlocklisted: %v", err)
	}
	if byHash == 0 {
		t.Error("the same payload was offered again from another indexer")
	}
}

// Auto entries expire because indexers repair things; a permanent block would
// make a transient failure permanent.
func TestExpiredBlocklistEntriesStopMatching(t *testing.T) {
	opened, queries := newLibrary(t)

	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		_, err := q.BlocklistRelease(ctx, librarydb.BlocklistReleaseParams{
			IndexerID: sql.NullInt64{Int64: 1, Valid: true},
			Guid:      sql.NullString{String: "guid-a", Valid: true},
			Title:     "stale", Reason: "STALLED", Source: "auto",
			ExpiresAt: sql.NullInt64{Int64: 100, Valid: true}, CreatedAt: 1,
		})
		return err
	})

	blocked, err := queries.IsBlocklisted(t.Context(), librarydb.IsBlocklistedParams{
		Now: sql.NullInt64{Int64: 500, Valid: true}, IndexerID: sql.NullInt64{Int64: 1, Valid: true},
		Guid: sql.NullString{String: "guid-a", Valid: true},
	})
	if err != nil {
		t.Fatalf("IsBlocklisted: %v", err)
	}
	if blocked != 0 {
		t.Error("an expired block still matched")
	}
}

// ADR-12: quarantined bytes are not free space. Counting them as free lets
// quarantine cause the disk-full condition it exists to prevent.
func TestQuarantinedBytesAreCountedPerRoot(t *testing.T) {
	opened, queries := newLibrary(t)

	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		for index, size := range []int64{1_000, 2_000} {
			if _, err := q.Quarantine(ctx, librarydb.QuarantineParams{
				OriginalPath:   fmt.Sprintf("/media/a%d.mkv", index),
				QuarantinePath: fmt.Sprintf("/media/.quarantine/a%d.mkv", index),
				RootFolderID:   sql.NullInt64{Int64: 1, Valid: true},
				Reason:         "rejected_worse", SizeBytes: size,
				QuarantinedAt: 1, ExpiresAt: 1_000,
			}); err != nil {
				return err
			}
		}
		// Another root's holdings must not be counted here.
		_, err := q.Quarantine(ctx, librarydb.QuarantineParams{
			OriginalPath: "/other/b.mkv", QuarantinePath: "/other/.quarantine/b.mkv",
			RootFolderID: sql.NullInt64{Int64: 2, Valid: true},
			Reason:       "junk", SizeBytes: 9_000, QuarantinedAt: 1, ExpiresAt: 1_000,
		})
		return err
	})

	held, err := queries.QuarantinedBytesForRoot(t.Context(),
		sql.NullInt64{Int64: 1, Valid: true})
	if err != nil {
		t.Fatalf("QuarantinedBytesForRoot: %v", err)
	}
	if held != 3_000 {
		t.Errorf("held %d bytes for the root, want 3000", held)
	}
}

// A restored file is no longer held, so it stops counting against the gate.
func TestRestoringReleasesTheHeldBytes(t *testing.T) {
	opened, queries := newLibrary(t)

	var entry librarydb.Quarantine
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		row, err := q.Quarantine(ctx, librarydb.QuarantineParams{
			OriginalPath: "/media/a.mkv", QuarantinePath: "/media/.quarantine/a.mkv",
			RootFolderID: sql.NullInt64{Int64: 1, Valid: true},
			Reason:       "user_delete", SizeBytes: 5_000, QuarantinedAt: 1, ExpiresAt: 100,
		})
		entry = row
		return err
	})
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.MarkQuarantineRestored(ctx, librarydb.MarkQuarantineRestoredParams{
			ID: entry.ID, RestoredAt: sql.NullInt64{Int64: 50, Valid: true},
		})
	})

	held, err := queries.QuarantinedBytesForRoot(t.Context(),
		sql.NullInt64{Int64: 1, Valid: true})
	if err != nil {
		t.Fatalf("QuarantinedBytesForRoot: %v", err)
	}
	if held != 0 {
		t.Errorf("a restored file still holds %d bytes", held)
	}
}

// F4: which search caused this file, and which download produced it. The
// acquisition denormalises the candidate because candidates expire.
func TestProvenanceSurvivesTheCandidateExpiring(t *testing.T) {
	opened, queries := newLibrary(t)
	work := createWork(t, opened, "movie", "Cige")
	item := createDesired(t, opened, work.ID, "SINGLE", 1)
	object := createObject(t, opened, "quick-1", 4096)

	var candidate librarydb.Candidate
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		row, err := q.UpsertCandidate(ctx, librarydb.UpsertCandidateParams{
			IndexerID: 1, Guid: "guid-a", Title: "Cige.2021.2160p.REMUX-GROUP",
			Protocol: "usenet", DownloadUrl: "https://indexer/nzb/1",
			Source: "search", FirstSeenAt: 1, ExpiresAt: 100,
		})
		candidate = row
		return err
	})

	var acquisition librarydb.Acquisition
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		row, err := q.CreateAcquisition(ctx, librarydb.CreateAcquisitionParams{
			DesiredItemID:  sql.NullInt64{Int64: item.ID, Valid: true},
			CandidateID:    sql.NullInt64{Int64: candidate.ID, Valid: true},
			IndexerID:      sql.NullInt64{Int64: 1, Valid: true},
			CandidateGuid:  sql.NullString{String: "guid-a", Valid: true},
			CandidateTitle: sql.NullString{String: "Cige.2021.2160p.REMUX-GROUP", Valid: true},
			SearchTerms:    sql.NullString{String: "dune 2021", Valid: true},
			DownloaderID:   1, DownloadClientRef: "nzo-1", Protocol: "usenet",
			State: "IMPORTED", CreatedAt: 2, UpdatedAt: 2,
		})
		acquisition = row
		return err
	})
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		_, err := q.RecordAcquiredFile(ctx, librarydb.RecordAcquiredFileParams{
			AcquisitionID: acquisition.ID, SourcePath: "/downloads/dune.mkv",
			MediaObjectID: sql.NullInt64{Int64: object.ID, Valid: true},
			ImportedAt:    sql.NullInt64{Int64: 3, Valid: true},
		})
		return err
	})

	// The candidate expires; the provenance must not go with it.
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.DeleteExpiredCandidates(ctx, 1_000)
	})

	chain, err := queries.ProvenanceForObject(t.Context(),
		sql.NullInt64{Int64: object.ID, Valid: true})
	if err != nil {
		t.Fatalf("ProvenanceForObject: %v", err)
	}
	if len(chain) != 1 {
		t.Fatalf("%d provenance rows, want 1", len(chain))
	}
	if chain[0].SearchTerms.String != "dune 2021" ||
		chain[0].CandidateTitle.String != "Cige.2021.2160p.REMUX-GROUP" {
		t.Errorf("provenance lost its detail when the candidate expired: %+v", chain[0])
	}
}

// The protocol enum has to admit what ADR-46 added, or podcasts and direct
// downloads cannot be recorded at all.
func TestEveryProtocolIsAccepted(t *testing.T) {
	opened, _ := newLibrary(t)

	for index, protocol := range []string{"usenet", "torrent", "ddl", "feed"} {
		err := opened.Write(t.Context(), store.PrioInteractive,
			func(ctx context.Context, tx *sql.Tx) error {
				q := librarydb.New(opened.Read()).WithTx(tx)
				_, err := q.UpsertCandidate(ctx, librarydb.UpsertCandidateParams{
					IndexerID: int64(index + 1), Guid: "g", Title: "t",
					Protocol: protocol, DownloadUrl: "u", Source: "search",
					FirstSeenAt: 1, ExpiresAt: 2,
				})
				return err
			})
		if err != nil {
			t.Errorf("protocol %q was refused: %v", protocol, err)
		}
	}
}
