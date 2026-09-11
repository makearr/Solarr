package library_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/makearr/MediaNexus/internal/db/library"
	"github.com/makearr/MediaNexus/internal/db/store"
	"github.com/makearr/MediaNexus/internal/db/store/gen/librarydb"
)

func TestAttributeClaim_HashIsDeterministic(t *testing.T) {
	claim := library.AttributeClaim{AudioCodec: "flac", Lossless: true, SampleRate: 44100}
	first := claim.Hash()
	second := claim.Hash()
	if first != second {
		t.Errorf("Hash() = %q then %q, want the same value across calls for the same claim", first, second)
	}
}

func TestAttributeClaim_HashDiffersOnDifferentFields(t *testing.T) {
	a := library.AttributeClaim{AudioCodec: "flac", SampleRate: 44100}
	b := library.AttributeClaim{AudioCodec: "flac", SampleRate: 48000}
	if a.Hash() == b.Hash() {
		t.Error("two claims differing only in sample rate hashed to the same value")
	}
}

// TestAttributes_InternReusesRowForIdenticalClaims proves section 5.2's own
// point of interning: two files claiming or measuring an identical set must
// land in the same media_attributes row, not one each.
func TestAttributes_InternReusesRowForIdenticalClaims(t *testing.T) {
	opened, _ := newLibrary(t)
	attributes := library.NewAttributes(opened)
	claim := library.AttributeClaim{AudioCodec: "flac", Lossless: true, SampleRate: 96000, BitDepth: 24}

	first, err := attributes.Intern(t.Context(), claim)
	if err != nil {
		t.Fatalf("Intern: %v", err)
	}
	second, err := attributes.Intern(t.Context(), claim)
	if err != nil {
		t.Fatalf("Intern: %v", err)
	}
	if first != second {
		t.Errorf("Intern(claim) = %d then %d, want the same row reused", first, second)
	}

	other, err := attributes.Intern(t.Context(), library.AttributeClaim{AudioCodec: "mp3", BitrateKbps: 320})
	if err != nil {
		t.Fatalf("Intern: %v", err)
	}
	if other == first {
		t.Error("a distinct claim reused the first claim's row")
	}
}

func TestPhysical_RecordMeasuredWritesAttributesAndScalars(t *testing.T) {
	opened, queries := newLibrary(t)
	physical := library.NewPhysical(opened)
	object := createObject(t, opened, "quickhash-1", 1000)

	err := physical.RecordMeasured(t.Context(), object.ID, library.AttributeClaim{
		AudioCodec: "flac", Lossless: true, SampleRate: 44100, BitDepth: 16,
	}, library.MeasuredScalars{
		DurationMs: 215000, AudioBitrate: 705600, OverallBitrate: 705600, ProbeJSON: `{"ok":true}`,
	}, "", 100)
	if err != nil {
		t.Fatalf("RecordMeasured: %v", err)
	}

	updated, err := queries.GetMediaObject(t.Context(), object.ID)
	if err != nil {
		t.Fatalf("GetMediaObject: %v", err)
	}
	if !updated.MeasuredAttributesID.Valid {
		t.Fatal("MeasuredAttributesID is not set")
	}
	if updated.DurationMs.Int64 != 215000 {
		t.Errorf("DurationMs = %d, want 215000", updated.DurationMs.Int64)
	}
	if updated.AttributeMismatchJson.Valid {
		t.Errorf("AttributeMismatchJson = %q, want NULL for an empty mismatch string", updated.AttributeMismatchJson.String)
	}

	attrs, err := queries.GetAttributes(t.Context(), updated.MeasuredAttributesID.Int64)
	if err != nil {
		t.Fatalf("GetAttributes: %v", err)
	}
	if attrs.AudioCodec.String != "flac" || attrs.SampleRate.Int64 != 44100 {
		t.Errorf("interned attributes = %+v, want flac/44100", attrs)
	}
}

// TestPhysical_RecordClaimedWritesInternedAttributes mirrors
// TestPhysical_RecordMeasuredWritesAttributesAndScalars -- the same
// intern-then-point shape, minus the scalar fields and mismatch comparison
// that only mean something once a measured claim exists too.
func TestPhysical_RecordClaimedWritesInternedAttributes(t *testing.T) {
	opened, queries := newLibrary(t)
	physical := library.NewPhysical(opened)
	object := createObject(t, opened, "quickhash-claimed-1", 1000)

	err := physical.RecordClaimed(t.Context(), object.ID, library.AttributeClaim{
		AudioCodec: "flac", Lossless: true, Container: "m4a", ReleaseGroup: "OBZEN",
	})
	if err != nil {
		t.Fatalf("RecordClaimed: %v", err)
	}

	updated, err := queries.GetMediaObject(t.Context(), object.ID)
	if err != nil {
		t.Fatalf("GetMediaObject: %v", err)
	}
	if !updated.ClaimedAttributesID.Valid {
		t.Fatal("ClaimedAttributesID is not set")
	}

	attrs, err := queries.GetAttributes(t.Context(), updated.ClaimedAttributesID.Int64)
	if err != nil {
		t.Fatalf("GetAttributes: %v", err)
	}
	if attrs.AudioCodec.String != "flac" || attrs.Container.String != "m4a" || attrs.ReleaseGroup.String != "OBZEN" {
		t.Errorf("interned attributes = %+v, want flac/m4a/OBZEN", attrs)
	}
}

func TestPhysical_ClaimedAttributesFor_NoneSetYet(t *testing.T) {
	opened, _ := newLibrary(t)
	physical := library.NewPhysical(opened)
	object := createObject(t, opened, "quickhash-2", 1000)

	_, found, err := physical.ClaimedAttributesFor(t.Context(), object.ID)
	if err != nil {
		t.Fatalf("ClaimedAttributesFor: %v", err)
	}
	if found {
		t.Error("found a claimed attribute row when nothing set claimed_attributes_id")
	}
}

// TestPhysical_ClaimedAttributesFor_ReadsWhatWasInterned proves the read
// side alone, independent of RecordClaimed's own producer test above -- the
// column is set directly here rather than through RecordClaimed.
func TestPhysical_ClaimedAttributesFor_ReadsWhatWasInterned(t *testing.T) {
	opened, _ := newLibrary(t)
	attributes := library.NewAttributes(opened)
	physical := library.NewPhysical(opened)
	object := createObject(t, opened, "quickhash-3", 1000)

	claimID, err := attributes.Intern(t.Context(), library.AttributeClaim{AudioCodec: "mp3", BitrateKbps: 320})
	if err != nil {
		t.Fatalf("Intern: %v", err)
	}
	if err := opened.Write(t.Context(), store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE media_object SET claimed_attributes_id = ? WHERE id = ?", claimID, object.ID)
		return err
	}); err != nil {
		t.Fatalf("set claimed_attributes_id: %v", err)
	}

	claim, found, err := physical.ClaimedAttributesFor(t.Context(), object.ID)
	if err != nil {
		t.Fatalf("ClaimedAttributesFor: %v", err)
	}
	if !found {
		t.Fatal("found = false, want true")
	}
	if claim.AudioCodec != "mp3" || claim.BitrateKbps != 320 {
		t.Errorf("claim = %+v, want AudioCodec=mp3 BitrateKbps=320", claim)
	}
}

func TestListUnmeasured_OnlyReturnsFilesWithNoMeasuredAttributes(t *testing.T) {
	opened, _ := newLibrary(t)
	physical := library.NewPhysical(opened)

	measured := createObject(t, opened, "measured-hash", 100)
	unmeasured := createObject(t, opened, "unmeasured-hash", 200)

	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		if _, err := q.AddLibraryPath(ctx, librarydb.AddLibraryPathParams{
			MediaObjectID: measured.ID, RootFolderID: 1, RelPath: "measured.flac",
			LinkKind: "primary", IsPrimary: 1, CreatedAt: 1,
		}); err != nil {
			return err
		}
		_, err := q.AddLibraryPath(ctx, librarydb.AddLibraryPathParams{
			MediaObjectID: unmeasured.ID, RootFolderID: 1, RelPath: "unmeasured.flac",
			LinkKind: "primary", IsPrimary: 1, CreatedAt: 1,
		})
		return err
	})

	if err := physical.RecordMeasured(t.Context(), measured.ID,
		library.AttributeClaim{AudioCodec: "flac"}, library.MeasuredScalars{}, "", 1); err != nil {
		t.Fatalf("RecordMeasured: %v", err)
	}

	files, err := physical.ListUnmeasured(t.Context(), 0, 100)
	if err != nil {
		t.Fatalf("ListUnmeasured: %v", err)
	}
	if len(files) != 1 || files[0].MediaObjectID != unmeasured.ID {
		t.Errorf("ListUnmeasured = %+v, want exactly the unmeasured object", files)
	}
}
