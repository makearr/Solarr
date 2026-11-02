package library_test

import (
	"context"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/identify/model"
)

var retiredHint = model.Identification{
	Strategy: "retired release group", Confidence: 0.2,
	ExternalID: &model.ExternalID{Provider: "musicbrainz:release-group", ExternalID: "rg-retired", Confidence: 0.2},
}

// albumWithFiles is a work holding two files on disk and its album item with both attached.
func albumWithFiles(t *testing.T, addedBy *int64) (int64, []int64, *librarydb.Queries, library.Identity, *store.Store) {
	t.Helper()
	opened, queries := newLibrary(t)
	work := createWork(t, opened, "music", "Detisisu Binip")
	registered, err := library.NewPhysical(opened).RegisterBatch(t.Context(), 1, []library.PhysicalFile{
		{RelPath: "Heka/Detisisu Binip/01.flac", SizeBytes: 1, QuickHash: "h1", FirstSeenAt: time.Unix(1, 0)},
		{RelPath: "Heka/Detisisu Binip/02.flac", SizeBytes: 1, QuickHash: "h2", FirstSeenAt: time.Unix(1, 0)},
	})
	if err != nil {
		t.Fatalf("RegisterBatch: %v", err)
	}
	item, err := library.NewDesired(opened).EnsureAlbumItem(t.Context(), library.AlbumRequest{
		WorkID: work.ID, CategoryID: 1, ProfileID: 1, Monitored: true, AddedBy: addedBy,
	})
	if err != nil {
		t.Fatalf("EnsureAlbumItem: %v", err)
	}
	ids := make([]int64, len(registered))
	for index, file := range registered {
		ids[index] = file.MediaObjectID
		write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
			return q.LinkWorkFile(ctx, librarydb.LinkWorkFileParams{WorkID: work.ID, MediaObjectID: file.MediaObjectID, FirstSeenAt: 1})
		})
		attach(t, opened, item.ID, file.MediaObjectID, "primary", string(rune('1'+index)))
	}
	return work.ID, ids, queries, library.NewIdentity(opened), opened
}

var localName = model.ExternalID{Provider: "local:album", ExternalID: "/music/heka/detisisu binip|detisisu binip", Confidence: 0.1}

// M1f: an album whose release group no longer exists anywhere goes back to Review file by file, with the retired id as a
// hint. Its identification is not taken away for nothing (live_test 36): it stays, as an unverified album, and the
// files' history says what happened.
func TestReturnToReview_KeepsTheAlbumUnverifiedWithItsFiles(t *testing.T) {
	workID, files, queries, identity, opened := albumWithFiles(t, nil)
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.LinkWorkExternalID(ctx, librarydb.LinkWorkExternalIDParams{Provider: "musicbrainz:release-group", ExternalID: "rg-retired", WorkID: workID, Confidence: 1, FirstSeenAt: 1})
	})

	returned, err := identity.ReturnToReview(t.Context(), workID, retiredHint, localName, 5)
	if err != nil || returned != (library.ReturnedToReview{Files: 2, Title: "Detisisu Binip", Unverified: true}) {
		t.Fatalf("ReturnToReview = %+v, %v; want 2 files, unverified", returned, err)
	}
	for _, mediaObjectID := range files {
		entry, err := queries.GetUnidentified(t.Context(), mediaObjectID)
		if err != nil || entry.CandidatesJson != `[{"strategy":"retired release group","provider":"musicbrainz:release-group","external_id":"rg-retired","confidence":0.2}]` {
			t.Errorf("queue entry for %d = %+v, %v; want the retired release group as a hint", mediaObjectID, entry, err)
		}
		attempts, err := queries.ListIdentificationAttempts(t.Context(), mediaObjectID)
		if err != nil || len(attempts) != 1 || attempts[0].Outcome != "queued" {
			t.Errorf("attempts for %d = %+v, %v; want one saying it was queued", mediaObjectID, attempts, err)
		}
	}
	if count, err := queries.CountWorkFiles(t.Context(), workID); err != nil || count != 2 {
		t.Errorf("work holds %d files, %v; want both still on it", count, err)
	}
	ids, err := queries.ListWorkExternalIDs(t.Context(), workID)
	if err != nil || len(ids) != 1 || ids[0].Provider != "local:album" {
		t.Errorf("external ids = %+v, %v; want the local name alone", ids, err)
	}
	items, err := queries.ListDesiredItemsForWork(t.Context(), workID)
	if err != nil || len(items) != 1 || items[0].Monitored != 0 {
		t.Errorf("items = %+v, %v; want the album unmonitored", items, err)
	}
}

// A work a person added stays wanted, now MISSING, so it can be searched for once identified again.
func TestReturnToReview_KeepsAWorkAPersonAdded(t *testing.T) {
	person := int64(1)
	workID, _, queries, identity, _ := albumWithFiles(t, &person)

	returned, err := identity.ReturnToReview(t.Context(), workID, retiredHint, localName, 5)
	if err != nil || returned != (library.ReturnedToReview{Files: 2, Title: "Detisisu Binip"}) {
		t.Fatalf("ReturnToReview = %+v, %v; want 2 files, work kept", returned, err)
	}
	items, err := queries.ListDesiredItemsForWork(t.Context(), workID)
	if err != nil || len(items) != 1 || items[0].State != "MISSING" || items[0].PresentCount != 0 {
		t.Errorf("items = %+v, %v; want the album item MISSING with nothing present", items, err)
	}
	if files, err := queries.CountWorkFiles(t.Context(), workID); err != nil || files != 0 {
		t.Errorf("work still holds %d files, %v", files, err)
	}
}

// D6: files of an unverified album are identified one at a time from Unidentified; the album keeps the
// rest until its last file leaves, then goes.
func TestLeaveUnverified_TheAlbumGoesWithItsLastFile(t *testing.T) {
	workID, files, queries, identity, opened := albumWithFiles(t, nil)
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.LinkWorkExternalID(ctx, librarydb.LinkWorkExternalIDParams{
			Provider: "local:album", ExternalID: "/music/unsorted|unsorted", WorkID: workID, Confidence: 0.1, FirstSeenAt: 1,
		})
	})
	if present, err := identity.AnyUnverified(t.Context()); err != nil || !present {
		t.Fatalf("AnyUnverified = %v, %v; want true", present, err)
	}

	if deleted, err := identity.LeaveUnverified(t.Context(), files[:1]); err != nil || len(deleted) != 0 {
		t.Fatalf("first file: deleted %v, %v; want the album kept", deleted, err)
	}
	if left, err := queries.CountWorkFiles(t.Context(), workID); err != nil || left != 1 {
		t.Fatalf("files left = %d, %v; want 1", left, err)
	}
	if deleted, err := identity.LeaveUnverified(t.Context(), files[1:]); err != nil || len(deleted) != 1 {
		t.Fatalf("last file: deleted %v, %v; want the album gone", deleted, err)
	}
}
