package library_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
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
	return readBack(t, opened, librarydb.CreateMediaObjectParams{
		SizeBytes: size, QuickHash: quickHash, FirstSeenAt: 1,
	})
}

// The insert returns an id rather than a row (documentation/benchmarks.md), so a test
// wanting the whole object reads it back.
func readBack(t *testing.T, opened *store.Store, params librarydb.CreateMediaObjectParams) librarydb.MediaObject {
	t.Helper()
	var id int64
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		created, err := q.CreateMediaObject(ctx, params)
		id = created
		return err
	})

	object, err := librarydb.New(opened.Read()).GetMediaObject(t.Context(), id)
	if err != nil {
		t.Fatalf("read back object %d: %v", id, err)
	}
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

// ADR-51's own RECORD step: media_object identity (Invariant 70) is
// untouched by a relocation -- only where it lives changes.
func TestRelocate_MovesTheLibraryPath(t *testing.T) {
	opened, queries := newLibrary(t)
	object := createObject(t, opened, "quick-1", 4096)
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		_, err := q.AddLibraryPath(ctx, librarydb.AddLibraryPathParams{
			MediaObjectID: object.ID, RootFolderID: 1, RelPath: "Old/track.flac",
			LinkKind: "primary", IsPrimary: 1, CreatedAt: 1,
		})
		return err
	})

	physical := library.NewPhysical(opened)
	if err := physical.Relocate(t.Context(), 1, "Old/track.flac", 2, "New/track.flac"); err != nil {
		t.Fatalf("Relocate: %v", err)
	}

	paths, err := queries.ListPathsForObject(t.Context(), object.ID)
	if err != nil {
		t.Fatalf("ListPathsForObject: %v", err)
	}
	if len(paths) != 1 || paths[0].RootFolderID != 2 || paths[0].RelPath != "New/track.flac" {
		t.Fatalf("paths = %+v, want the one path moved to root 2 / New/track.flac", paths)
	}
	if paths[0].MediaObjectID != object.ID {
		t.Error("media_object_id changed across a relocation -- Invariant 70 violated")
	}
}

// A relocation naming a location that no longer exists (a concurrent
// change since Plan ran) must fail loudly, not silently do nothing.
func TestRelocate_FailsWhenTheSourceLocationIsGone(t *testing.T) {
	opened, _ := newLibrary(t)
	physical := library.NewPhysical(opened)
	err := physical.Relocate(t.Context(), 1, "Nowhere/track.flac", 2, "New/track.flac")
	if !errors.Is(err, library.ErrPathNotFound) {
		t.Errorf("Relocate = %v, want ErrPathNotFound", err)
	}
}

// PathsForRoot is ADR-51 drain's own file enumeration: every real file on a
// root, size included, primary paths only. A secondary path on a different
// root for the same object is correctly excluded.
func TestPathsForRoot_ReturnsEveryPrimaryFileOnThatRootWithSize(t *testing.T) {
	opened, _ := newLibrary(t)
	onTarget := createObject(t, opened, "quick-1", 100)
	elsewhere := createObject(t, opened, "quick-2", 200)
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		if _, err := q.AddLibraryPath(ctx, librarydb.AddLibraryPathParams{
			MediaObjectID: onTarget.ID, RootFolderID: 5, RelPath: "Artist/track.flac",
			LinkKind: "primary", IsPrimary: 1, CreatedAt: 1,
		}); err != nil {
			return err
		}
		_, err := q.AddLibraryPath(ctx, librarydb.AddLibraryPathParams{
			MediaObjectID: elsewhere.ID, RootFolderID: 9, RelPath: "Other/track.flac",
			LinkKind: "primary", IsPrimary: 1, CreatedAt: 1,
		})
		return err
	})

	physical := library.NewPhysical(opened)
	files, err := physical.PathsForRoot(t.Context(), 5)
	if err != nil {
		t.Fatalf("PathsForRoot: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("files = %+v, want exactly the one file on root 5", files)
	}
	if files[0].MediaObjectID != onTarget.ID || files[0].RelPath != "Artist/track.flac" || files[0].SizeBytes != 100 {
		t.Errorf("file = %+v, want {%d, Artist/track.flac, 100}", files[0], onTarget.ID)
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

	object := readBack(t, opened, librarydb.CreateMediaObjectParams{
		SizeBytes: 1, QuickHash: "quick-1", FirstSeenAt: 1,
		ClaimedAttributesID: sql.NullInt64{Int64: claimed.ID, Valid: true},
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

// The reason the measured scalars are not interned. Two rips of the same film at
// the same quality differ by a few seconds and a few kbps; with duration and
// bitrate in the interned set every file would mint its own attribute row and the
// table would grow one-for-one with the library.
func TestFilesDifferingOnlyInDurationShareOneAttributeRow(t *testing.T) {
	opened, queries := newLibrary(t)

	attributes := intern(t, opened, "hash-1080p-webdl-x264", "1080p")

	var objects []librarydb.MediaObject
	for index, measured := range []struct {
		duration int64
		bitrate  int64
	}{{7_281_344, 8_412_000}, {7_283_991, 8_407_500}, {7_280_002, 8_419_250}} {
		objects = append(objects, readBack(t, opened, librarydb.CreateMediaObjectParams{
			SizeBytes: 7 << 30, QuickHash: fmt.Sprintf("quick-rip-%d", index),
			FirstSeenAt:          1,
			MeasuredAttributesID: sql.NullInt64{Int64: attributes.ID, Valid: true},
			DurationMs:           sql.NullInt64{Int64: measured.duration, Valid: true},
			OverallBitrate:       sql.NullInt64{Int64: measured.bitrate, Valid: true},
			Width:                sql.NullInt64{Int64: 1920, Valid: true},
			Height:               sql.NullInt64{Int64: 804, Valid: true},
		}))
	}

	for _, object := range objects {
		if object.MeasuredAttributesID.Int64 != attributes.ID {
			t.Errorf("object %d interned as attribute row %d, want the shared %d",
				object.ID, object.MeasuredAttributesID.Int64, attributes.ID)
		}
	}

	// Each file kept its own measurements even though they share the attribute row.
	durations := map[int64]bool{}
	for _, object := range objects {
		stored, err := queries.GetMediaObject(t.Context(), object.ID)
		if err != nil {
			t.Fatalf("GetMediaObject: %v", err)
		}
		if !stored.DurationMs.Valid || !stored.OverallBitrate.Valid {
			t.Fatalf("object %d lost its measurements: %+v", object.ID, stored)
		}
		durations[stored.DurationMs.Int64] = true
	}
	if len(durations) != 3 {
		t.Errorf("the three rips report %d distinct durations, want 3", len(durations))
	}
}

// Revision 2, D3: a scan registers each file with its kind, and the rows from before the column get theirs once.
func TestClassifyKinds_GivesEveryUnclassifiedObjectTheKindOfItsPath(t *testing.T) {
	opened, queries := newLibrary(t)
	physical := library.NewPhysical(opened)
	registered, err := physical.RegisterBatch(t.Context(), 1, []library.PhysicalFile{
		{RelPath: "Show/S01E01.mkv", SizeBytes: 10, QuickHash: "registered", FirstSeenAt: time.Unix(1, 0)},
	})
	if err != nil {
		t.Fatalf("RegisterBatch: %v", err)
	}
	unclassified := map[string]string{"A/01.flac": "audio", "B/film.mp4": "video", "B/film.en.srt": "subtitle", "A/cover.jpg": "image", "A/notes.nfo": "other"}
	ids := map[string]int64{}
	for relPath := range unclassified {
		object := createObject(t, opened, "quick-"+relPath, 1)
		ids[relPath] = object.ID
		write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
			_, err := q.AddLibraryPath(ctx, librarydb.AddLibraryPathParams{
				MediaObjectID: object.ID, RootFolderID: 1, RelPath: relPath, LinkKind: "primary", IsPrimary: 1, CreatedAt: 1,
			})
			return err
		})
	}

	classified, err := physical.ClassifyKinds(t.Context())
	if err != nil || classified != len(unclassified) {
		t.Fatalf("ClassifyKinds = %d, %v; want %d", classified, err, len(unclassified))
	}
	got := map[string]string{}
	for relPath, id := range ids {
		object, err := queries.GetMediaObject(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		got[relPath] = object.Kind.String
	}
	if fmt.Sprint(got) != fmt.Sprint(unclassified) {
		t.Errorf("kinds = %v, want %v", got, unclassified)
	}
	object, err := queries.GetMediaObject(t.Context(), registered[0].MediaObjectID)
	if err != nil || object.Kind != (sql.NullString{String: "video", Valid: true}) {
		t.Errorf("registered kind = %+v, %v; want video", object.Kind, err)
	}
	if again, err := physical.ClassifyKinds(t.Context()); again != 0 || err != nil {
		t.Errorf("a second pass = %d, %v; want 0", again, err)
	}
}

// Revision 2, D3: the video re-probe picks its files by kind, so a stale audio probe is left alone.
func TestListStaleVideoProbes_FindsOnlyVideo(t *testing.T) {
	opened, _ := newLibrary(t)
	physical := library.NewPhysical(opened)
	registered, err := physical.RegisterBatch(t.Context(), 1, []library.PhysicalFile{
		{RelPath: "Show/S01E01.mkv", SizeBytes: 10, QuickHash: "video", FirstSeenAt: time.Unix(1, 0)},
		{RelPath: "Artist/01.flac", SizeBytes: 10, QuickHash: "audio", FirstSeenAt: time.Unix(1, 0)},
	})
	if err != nil {
		t.Fatalf("RegisterBatch: %v", err)
	}
	attributes := intern(t, opened, "hash-measured", "")
	err = opened.Write(t.Context(), store.PrioNormal, func(ctx context.Context, tx *sql.Tx) error {
		for _, file := range registered {
			if _, err := tx.ExecContext(ctx, `UPDATE media_object SET measured_attributes_id = ? WHERE id = ?`, attributes.ID, file.MediaObjectID); err != nil {
				return err
			}
			if err := librarydb.New(tx).SetMediaProbe(ctx, librarydb.SetMediaProbeParams{
				MediaObjectID: file.MediaObjectID, ProbeVersion: library.ProbeVersionLyrics, ProbedAt: 1, Json: "{}",
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	stale, err := physical.ListStaleVideoProbes(t.Context(), 0, 10)
	want := []library.UnmeasuredFile{{MediaObjectID: registered[0].MediaObjectID, RootFolderID: 1, RelPath: "Show/S01E01.mkv"}}
	if err != nil || fmt.Sprint(stale) != fmt.Sprint(want) {
		t.Errorf("ListStaleVideoProbes = %+v, %v; want %+v", stale, err, want)
	}
}
