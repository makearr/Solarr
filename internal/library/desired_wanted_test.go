package library_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/makearr/MediaNexus/internal/library"
	"github.com/makearr/MediaNexus/internal/store/gen/librarydb"
)

func TestEnsureAlbumItem_CreatesASetItemWithNoRequiredCountBeforeAnyPartsExist(t *testing.T) {
	opened, _ := newLibrary(t)
	desired := library.NewDesired(opened)
	work := createWork(t, opened, "music", "Tender Prey")

	item, err := desired.EnsureAlbumItem(t.Context(), library.AlbumRequest{
		WorkID: work.ID, CategoryID: 1, ProfileID: 1, Monitored: true,
	})
	if err != nil {
		t.Fatalf("EnsureAlbumItem: %v", err)
	}
	if item.SatisfactionKind != "SET" || item.EditionID.Valid || item.MediaRef != "" {
		t.Fatalf("unexpected item shape: %+v", item)
	}
	if item.RequiredCount.Valid {
		t.Errorf("want required_count unknown before any work_part exists, got %v", item.RequiredCount)
	}
}

// The single most-reported failure of discography monitoring elsewhere
// (Invariant 71, one level down): calling Ensure twice must never create a
// second album item nor touch the first one's own policy.
func TestEnsureAlbumItem_IsIdempotentAndNeverOverwritesAnExistingItemsPolicy(t *testing.T) {
	opened, queries := newLibrary(t)
	desired := library.NewDesired(opened)
	work := createWork(t, opened, "music", "Tender Prey")

	first, err := desired.EnsureAlbumItem(t.Context(), library.AlbumRequest{
		WorkID: work.ID, CategoryID: 1, ProfileID: 1, Monitored: true,
	})
	if err != nil {
		t.Fatalf("first EnsureAlbumItem: %v", err)
	}
	if err := desired.SetItemMonitored(t.Context(), first.ID, false); err != nil {
		t.Fatalf("SetItemMonitored: %v", err)
	}

	second, err := desired.EnsureAlbumItem(t.Context(), library.AlbumRequest{
		WorkID: work.ID, CategoryID: 1, ProfileID: 1, Monitored: true,
	})
	if err != nil {
		t.Fatalf("second EnsureAlbumItem: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("want the same item reused, got a second one: %d vs %d", first.ID, second.ID)
	}

	items, err := queries.ListDesiredItemsForWork(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("ListDesiredItemsForWork: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("want exactly one album item, got %d", len(items))
	}
	if items[0].Monitored != 0 {
		t.Error("the user's unmonitor must survive a later discovery pass")
	}
}

func TestRecordAlignment_NineOfTwelveTracksIsARealFactNotABadge(t *testing.T) {
	opened, _ := newLibrary(t)
	desired := library.NewDesired(opened)
	work := createWork(t, opened, "music", "Tender Prey")

	for track := 1; track <= 12; track++ {
		createPart(t, opened, work.ID, mediaRefFor(track), sortKeyFor(track), titleFor(track))
	}

	item, err := desired.EnsureAlbumItem(t.Context(), library.AlbumRequest{
		WorkID: work.ID, CategoryID: 1, ProfileID: 1, Monitored: true,
	})
	if err != nil {
		t.Fatalf("EnsureAlbumItem: %v", err)
	}
	if item.RequiredCount.Int64 != 12 || !item.RequiredCount.Valid {
		t.Fatalf("want required_count 12 now that parts exist, got %+v", item.RequiredCount)
	}

	var aligned []library.AlignedFile
	for track := 1; track <= 9; track++ {
		object := createObject(t, opened, mediaRefFor(track), 4096)
		aligned = append(aligned, library.AlignedFile{MediaObjectID: object.ID, MediaRef: mediaRefFor(track)})
	}
	if err := desired.RecordAlignment(t.Context(), item.ID, work.ID, aligned, 1); err != nil {
		t.Fatalf("RecordAlignment: %v", err)
	}

	updated, err := librarydb.New(opened.Read()).GetDesiredItem(t.Context(), item.ID)
	if err != nil {
		t.Fatalf("GetDesiredItem: %v", err)
	}
	if updated.PresentCount != 9 {
		t.Errorf("want present_count 9, got %d", updated.PresentCount)
	}
	if updated.State != "PARTIAL" {
		t.Errorf("want state PARTIAL for 9 of 12, got %s", updated.State)
	}

	unfilled, err := desired.UnfilledParts(t.Context(), item.ID, work.ID)
	if err != nil {
		t.Fatalf("UnfilledParts: %v", err)
	}
	if len(unfilled) != 3 {
		t.Fatalf("want the 3 specific missing tracks named, got %d", len(unfilled))
	}
	for index, want := range []string{"disc1:track10", "disc1:track11", "disc1:track12"} {
		if unfilled[index].MediaRef != want {
			t.Errorf("missing track %d: want %s, got %s", index, want, unfilled[index].MediaRef)
		}
	}
}

func TestRecordAlignment_CompletesWhenEveryPartIsCovered(t *testing.T) {
	opened, _ := newLibrary(t)
	desired := library.NewDesired(opened)
	work := createWork(t, opened, "music", "Gikoninah")
	createPart(t, opened, work.ID, "disc1:track1", "00010001", "Cosila Like Ceme Pomito")

	item, err := desired.EnsureAlbumItem(t.Context(), library.AlbumRequest{
		WorkID: work.ID, CategoryID: 1, ProfileID: 1, Monitored: true,
	})
	if err != nil {
		t.Fatalf("EnsureAlbumItem: %v", err)
	}

	object := createObject(t, opened, "quick-1", 4096)
	err = desired.RecordAlignment(t.Context(), item.ID, work.ID,
		[]library.AlignedFile{{MediaObjectID: object.ID, MediaRef: "disc1:track1"}}, 1)
	if err != nil {
		t.Fatalf("RecordAlignment: %v", err)
	}

	updated, err := librarydb.New(opened.Read()).GetDesiredItem(t.Context(), item.ID)
	if err != nil {
		t.Fatalf("GetDesiredItem: %v", err)
	}
	if updated.State != "COMPLETE" {
		t.Errorf("want COMPLETE with every part covered, got %s", updated.State)
	}
}

// Appendix E D3's shape, one level up: unmonitoring a bonus track shrinks the
// album rather than holding it PARTIAL forever.
func TestSetPartMonitored_UnmonitoringATrackShrinksRequiredCount(t *testing.T) {
	opened, _ := newLibrary(t)
	desired := library.NewDesired(opened)
	work := createWork(t, opened, "music", "Tender Prey")
	createPart(t, opened, work.ID, "disc1:track1", "00010001", "Track One")
	bonus := createPart(t, opened, work.ID, "disc1:track2", "00010002", "Bonus Track")

	item, err := desired.EnsureAlbumItem(t.Context(), library.AlbumRequest{
		WorkID: work.ID, CategoryID: 1, ProfileID: 1, Monitored: true,
	})
	if err != nil {
		t.Fatalf("EnsureAlbumItem: %v", err)
	}
	object := createObject(t, opened, "quick-1", 4096)
	if err := desired.RecordAlignment(t.Context(), item.ID, work.ID,
		[]library.AlignedFile{{MediaObjectID: object.ID, MediaRef: "disc1:track1"}}, 1); err != nil {
		t.Fatalf("RecordAlignment: %v", err)
	}

	if err := desired.SetPartMonitored(t.Context(), work.ID, bonus.ID, false); err != nil {
		t.Fatalf("SetPartMonitored: %v", err)
	}

	updated, err := librarydb.New(opened.Read()).GetDesiredItem(t.Context(), item.ID)
	if err != nil {
		t.Fatalf("GetDesiredItem: %v", err)
	}
	if updated.RequiredCount.Int64 != 1 {
		t.Errorf("want required_count 1 after unmonitoring the bonus track, got %d", updated.RequiredCount.Int64)
	}
	if updated.State != "COMPLETE" {
		t.Errorf("want COMPLETE once the only monitored track is covered, got %s", updated.State)
	}
}

func TestEnsureTrackItem_ResolvesAgainstARealPartNeverAGuessedIndex(t *testing.T) {
	opened, _ := newLibrary(t)
	desired := library.NewDesired(opened)
	work := createWork(t, opened, "music", "Tender Prey")
	createPart(t, opened, work.ID, "disc1:track5", "00010005", "Deanna")

	item, err := desired.EnsureTrackItem(t.Context(), library.TrackRequest{
		WorkID: work.ID, MediaRef: "disc1:track5", CategoryID: 1, ProfileID: 1,
	})
	if err != nil {
		t.Fatalf("EnsureTrackItem: %v", err)
	}
	if item.SatisfactionKind != "SINGLE" || item.MediaRef != "disc1:track5" {
		t.Fatalf("unexpected item shape: %+v", item)
	}

	_, err = desired.EnsureTrackItem(t.Context(), library.TrackRequest{
		WorkID: work.ID, MediaRef: "disc1:track99", CategoryID: 1, ProfileID: 1,
	})
	if !errors.Is(err, library.ErrPartNotFound) {
		t.Fatalf("want ErrPartNotFound for a media_ref with no work_part, got %v", err)
	}
}

func mediaRefFor(track int) string { return fmt.Sprintf("disc1:track%d", track) }
func sortKeyFor(track int) string  { return fmt.Sprintf("0001%04d", track) }
func titleFor(track int) string    { return fmt.Sprintf("Track %d", track) }
