package library_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/internal/store/gen/librarydb"
)

func intern(t *testing.T, opened *store.Store, hash, resolution string) librarydb.MediaAttribute {
	t.Helper()
	var attributes librarydb.MediaAttribute
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		row, err := q.InternAttributes(ctx, librarydb.InternAttributesParams{
			AttrHash:   hash,
			Resolution: sql.NullString{String: resolution, Valid: resolution != ""},
		})
		attributes = row
		return err
	})
	return attributes
}

func createObject(t *testing.T, opened *store.Store, quickHash string, size int64) librarydb.MediaObject {
	t.Helper()
	var object librarydb.MediaObject
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		row, err := q.CreateMediaObject(ctx, librarydb.CreateMediaObjectParams{
			SizeBytes: size, QuickHash: quickHash, FirstSeenAt: 1,
		})
		object = row
		return err
	})
	return object
}

// Interning is what keeps the attribute set small at 500k files: the same
// combination must return the same row rather than accumulating duplicates.
func TestAttributesAreInternedNotDuplicated(t *testing.T) {
	opened, _ := newLibrary(t)

	first := intern(t, opened, "hash-2160p-remux", "2160p")
	second := intern(t, opened, "hash-2160p-remux", "2160p")
	other := intern(t, opened, "hash-1080p-webdl", "1080p")

	if first.ID != second.ID {
		t.Errorf("the same attributes interned as %d and %d", first.ID, second.ID)
	}
	if other.ID == first.ID {
		t.Error("different attributes shared a row")
	}
}

// ADR-7 and ADR-11: one set of bytes, many paths. A hard-linked file must not
// become a second object, or the library counts it twice and the duplicate
// finder reports it against itself.
func TestOneObjectCarriesManyPaths(t *testing.T) {
	opened, queries := newLibrary(t)
	object := createObject(t, opened, "quick-1", 4096)

	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		for index, location := range []struct {
			root int64
			path string
			kind string
		}{
			{1, "Music/Nick Cave/Tender Prey/01.flac", "primary"},
			{2, "Backup/Tender Prey/01.flac", "hardlink"},
		} {
			_, err := q.AddLibraryPath(ctx, librarydb.AddLibraryPathParams{
				MediaObjectID: object.ID, RootFolderID: location.root,
				RelPath: location.path, LinkKind: location.kind,
				IsPrimary: int64(1 - index), CreatedAt: 1,
			})
			if err != nil {
				return err
			}
		}
		return nil
	})

	paths, err := queries.ListPathsForObject(t.Context(), object.ID)
	if err != nil {
		t.Fatalf("ListPathsForObject: %v", err)
	}
	if len(paths) != 2 {
		t.Fatalf("%d paths, want 2", len(paths))
	}
	// Primary first, so a caller that wants "the" path takes the first row.
	if paths[0].IsPrimary != 1 {
		t.Error("the primary path is not first")
	}
}

// Two files cannot occupy one location. Without this a rescan that re-imports a
// directory silently doubles the library.
func TestALocationIsUniqueWithinItsRoot(t *testing.T) {
	opened, _ := newLibrary(t)
	first := createObject(t, opened, "quick-1", 10)
	second := createObject(t, opened, "quick-2", 20)

	add := func(objectID int64) error {
		return opened.Write(t.Context(), store.PrioInteractive,
			func(ctx context.Context, tx *sql.Tx) error {
				q := librarydb.New(opened.Read()).WithTx(tx)
				_, err := q.AddLibraryPath(ctx, librarydb.AddLibraryPathParams{
					MediaObjectID: objectID, RootFolderID: 1,
					RelPath: "Music/same.flac", LinkKind: "primary", CreatedAt: 1,
				})
				return err
			})
	}
	if err := add(first.ID); err != nil {
		t.Fatalf("first path: %v", err)
	}
	if err := add(second.ID); err == nil {
		t.Error("two objects were stored at the same location")
	}
}

// ADR-39: the release name produces claims, the probe produces facts, and both
// are kept so a mismatch remains visible and can feed group reputation.
func TestClaimedAndMeasuredAttributesBothSurvive(t *testing.T) {
	opened, queries := newLibrary(t)
	claimed := intern(t, opened, "claimed-2160p", "2160p")
	measured := intern(t, opened, "measured-1080p", "1080p")

	var object librarydb.MediaObject
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		row, err := q.CreateMediaObject(ctx, librarydb.CreateMediaObjectParams{
			SizeBytes: 1, QuickHash: "quick-1", FirstSeenAt: 1,
			ClaimedAttributesID: sql.NullInt64{Int64: claimed.ID, Valid: true},
		})
		object = row
		return err
	})

	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.SetMeasuredAttributes(ctx, librarydb.SetMeasuredAttributesParams{
			ID:                    object.ID,
			MeasuredAttributesID:  sql.NullInt64{Int64: measured.ID, Valid: true},
			AttributeMismatchJson: sql.NullString{String: `["resolution"]`, Valid: true},
			VerifiedAt:            sql.NullInt64{Int64: 2, Valid: true},
		})
	})

	after, err := queries.GetMediaObject(t.Context(), object.ID)
	if err != nil {
		t.Fatalf("GetMediaObject: %v", err)
	}
	if after.ClaimedAttributesID.Int64 != claimed.ID {
		t.Error("the claim was overwritten; the mismatch is no longer visible")
	}
	if after.MeasuredAttributesID.Int64 != measured.ID {
		t.Error("the measured attributes were not recorded")
	}
	if after.AttributeMismatchJson.String == "" {
		t.Error("the mismatch itself was not recorded")
	}
}

// Fingerprinting is the lowest-priority background work and must be resumable,
// so it asks for the next unfingerprinted rows rather than scanning (ADR-35).
func TestFingerprintingResumesWhereItStopped(t *testing.T) {
	opened, queries := newLibrary(t)
	for index := range 5 {
		createObject(t, opened, "quick-"+string(rune('a'+index)), int64(index))
	}

	first, err := queries.ListUnfingerprinted(t.Context(),
		librarydb.ListUnfingerprintedParams{AfterID: 0, PageSize: 2})
	if err != nil {
		t.Fatalf("ListUnfingerprinted: %v", err)
	}
	if len(first) != 2 {
		t.Fatalf("%d rows, want 2", len(first))
	}

	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		for _, row := range first {
			if err := q.SetFingerprints(ctx, librarydb.SetFingerprintsParams{
				ID:              row.ID,
				Oshash:          sql.NullString{String: "os-hash", Valid: true},
				FingerprintedAt: sql.NullInt64{Int64: 9, Valid: true},
			}); err != nil {
				return err
			}
		}
		return nil
	})

	// Done work does not come back, so a restart makes progress rather than
	// re-fingerprinting from the beginning.
	remaining, err := queries.ListUnfingerprinted(t.Context(),
		librarydb.ListUnfingerprintedParams{AfterID: 0, PageSize: 10})
	if err != nil {
		t.Fatalf("ListUnfingerprinted: %v", err)
	}
	if len(remaining) != 3 {
		t.Errorf("%d rows still queued, want 3", len(remaining))
	}
}

// oshash is exact identity with no decoding, and is how the same bytes at a new
// path are recognised without reading them again (ADR-35).
func TestOshashResolvesToItsObject(t *testing.T) {
	opened, queries := newLibrary(t)
	object := createObject(t, opened, "quick-1", 4096)

	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.SetFingerprints(ctx, librarydb.SetFingerprintsParams{
			ID:              object.ID,
			Oshash:          sql.NullString{String: "abc123", Valid: true},
			FingerprintedAt: sql.NullInt64{Int64: 5, Valid: true},
		})
	})

	found, err := queries.GetMediaObjectByOshash(t.Context(),
		sql.NullString{String: "abc123", Valid: true})
	if err != nil {
		t.Fatalf("GetMediaObjectByOshash: %v", err)
	}
	if found.ID != object.ID {
		t.Errorf("resolved to %d, want %d", found.ID, object.ID)
	}
}

// Removing the bytes removes the paths that pointed at them.
func TestDeletingAnObjectCascadesToItsPaths(t *testing.T) {
	opened, queries := newLibrary(t)
	object := createObject(t, opened, "quick-1", 10)

	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		_, err := q.AddLibraryPath(ctx, librarydb.AddLibraryPathParams{
			MediaObjectID: object.ID, RootFolderID: 1, RelPath: "a.flac",
			LinkKind: "primary", IsPrimary: 1, CreatedAt: 1,
		})
		return err
	})
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.DeleteMediaObject(ctx, object.ID)
	})

	paths, err := queries.ListPathsForObject(t.Context(), object.ID)
	if err != nil {
		t.Fatalf("ListPathsForObject: %v", err)
	}
	if len(paths) != 0 {
		t.Errorf("%d paths survived their object", len(paths))
	}
}
