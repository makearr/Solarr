package library_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

func TestEnsureAlbumItem_CreatesASetItemWithNoRequiredCountBeforeAnyPartsExist(t *testing.T) {
	opened, _ := newLibrary(t)
	desired := library.NewDesired(opened)
	work := createWork(t, opened, "music", "Quiet Harbour")

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
	work := createWork(t, opened, "music", "Quiet Harbour")

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
	work := createWork(t, opened, "music", "Quiet Harbour")

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
	work := createWork(t, opened, "music", "Quiet Harbour")
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

func TestSetPartMonitored_RejectsAPartFromAnotherWork(t *testing.T) {
	opened, _ := newLibrary(t)
	desired := library.NewDesired(opened)
	workA := createWork(t, opened, "music", "Album A")
	workB := createWork(t, opened, "music", "Album B")
	partOfB := createPart(t, opened, workB.ID, "disc1:track1", "00010001", "Track One")

	if err := desired.SetPartMonitored(t.Context(), workA.ID, partOfB.ID, false); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("want sql.ErrNoRows for a part belonging to a different work, got %v", err)
	}
}

func TestSetPartMonitored_RejectsAMissingPart(t *testing.T) {
	opened, _ := newLibrary(t)
	desired := library.NewDesired(opened)
	work := createWork(t, opened, "music", "Album A")

	if err := desired.SetPartMonitored(t.Context(), work.ID, 99999, false); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("want sql.ErrNoRows for a nonexistent part, got %v", err)
	}
}

func TestEnsureTrackItem_ResolvesAgainstARealPartNeverAGuessedIndex(t *testing.T) {
	opened, _ := newLibrary(t)
	desired := library.NewDesired(opened)
	work := createWork(t, opened, "music", "Quiet Harbour")
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

// The API-facing entry point (todo 4.4): a caller has the part id from the
// tracklist, not the raw media_ref EnsureTrackItem itself requires.
func TestEnsureTrackItemByPart_ResolvesThePartIDToItsOwnMediaRef(t *testing.T) {
	opened, _ := newLibrary(t)
	desired := library.NewDesired(opened)
	work := createWork(t, opened, "music", "Quiet Harbour")
	part := createPart(t, opened, work.ID, "disc1:track5", "00010005", "Deanna")

	item, err := desired.EnsureTrackItemByPart(t.Context(), work.ID, part.ID, 1, 1, nil)
	if err != nil {
		t.Fatalf("EnsureTrackItemByPart: %v", err)
	}
	if item.SatisfactionKind != "SINGLE" || item.MediaRef != "disc1:track5" {
		t.Fatalf("unexpected item shape: %+v", item)
	}

	// Idempotent, the same as EnsureTrackItem itself: a second call reuses
	// the item rather than creating a sibling one (Invariant 71).
	again, err := desired.EnsureTrackItemByPart(t.Context(), work.ID, part.ID, 1, 1, nil)
	if err != nil {
		t.Fatalf("second EnsureTrackItemByPart: %v", err)
	}
	if again.ID != item.ID {
		t.Fatalf("want the same item reused, got a second one: %d vs %d", item.ID, again.ID)
	}
}

func TestEnsureTrackItemByPart_RejectsAPartFromAnotherWork(t *testing.T) {
	opened, _ := newLibrary(t)
	desired := library.NewDesired(opened)
	workA := createWork(t, opened, "music", "Album A")
	workB := createWork(t, opened, "music", "Album B")
	partOfB := createPart(t, opened, workB.ID, "disc1:track1", "00010001", "Track One")

	if _, err := desired.EnsureTrackItemByPart(t.Context(), workA.ID, partOfB.ID, 1, 1, nil); !errors.Is(err, library.ErrPartNotFound) {
		t.Fatalf("want ErrPartNotFound for a part belonging to a different work, got %v", err)
	}
}

func TestEnsureTrackItemByPart_RejectsAMissingPart(t *testing.T) {
	opened, _ := newLibrary(t)
	desired := library.NewDesired(opened)
	work := createWork(t, opened, "music", "Album A")

	if _, err := desired.EnsureTrackItemByPart(t.Context(), work.ID, 99999, 1, 1, nil); !errors.Is(err, library.ErrPartNotFound) {
		t.Fatalf("want ErrPartNotFound for a nonexistent part, got %v", err)
	}
}

func mediaRefFor(track int) string { return fmt.Sprintf("disc1:track%d", track) }
func sortKeyFor(track int) string  { return fmt.Sprintf("0001%04d", track) }
func titleFor(track int) string    { return fmt.Sprintf("Track %d", track) }

// PERF-12: unmonitoring an episode recomputes the season it is in and leaves the other seasons alone.
func TestSetPartsMonitored_RecomputesOnlyTheItemsThePartsBelongTo(t *testing.T) {
	opened, _ := newLibrary(t)
	desired := library.NewDesired(opened)
	work := createWork(t, opened, "tv", "Example Show")
	first := createPart(t, opened, work.ID, "s01:e01", "0101", "One")
	createPart(t, opened, work.ID, "s02:e01", "0201", "Two")
	items := map[string]librarydb.DesiredItem{}
	for _, ref := range []string{"s01", "s02"} {
		item, err := desired.EnsureSeasonItem(t.Context(), library.SeasonRequest{WorkID: work.ID, MediaRef: ref, SortKey: ref, CategoryID: 1, ProfileID: 1, Monitored: true})
		if err != nil {
			t.Fatal(err)
		}
		items[ref] = item
		if err := opened.Write(t.Context(), store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, "UPDATE desired_item SET dirty = 0 WHERE id = ?", item.ID)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}

	if err := desired.SetPartsMonitored(t.Context(), work.ID, []int64{first.ID}, false); err != nil {
		t.Fatal(err)
	}
	dirty := func(ref string) int64 {
		row, err := librarydb.New(opened.Read()).GetDesiredItem(t.Context(), items[ref].ID)
		if err != nil {
			t.Fatal(err)
		}
		return row.Dirty
	}
	if dirty("s01") != 1 || dirty("s02") != 0 {
		t.Errorf("dirty s01=%d s02=%d, want 1 and 0: only the season of the part is recomputed", dirty("s01"), dirty("s02"))
	}
}

// PERF-12: more parts than one write takes are all changed, in chunks of 200.
func TestSetPartsMonitored_ChangesEveryPartOfSeveralChunks(t *testing.T) {
	opened, _ := newLibrary(t)
	desired := library.NewDesired(opened)
	work := createWork(t, opened, "tv", "Example Show")
	var ids []int64
	for number := range 450 {
		part := createPart(t, opened, work.ID, fmt.Sprintf("s01:e%03d", number+1), fmt.Sprintf("%04d", number+1), "Episode")
		ids = append(ids, part.ID)
	}
	if err := desired.SetPartsMonitored(t.Context(), work.ID, ids, false); err != nil {
		t.Fatal(err)
	}
	parts, err := librarydb.New(opened.Read()).ListWorkParts(t.Context(), work.ID)
	if err != nil {
		t.Fatal(err)
	}
	monitored := 0
	for _, part := range parts {
		monitored += int(part.Monitored)
	}
	if len(parts) != 450 || monitored != 0 {
		t.Errorf("%d parts, %d still monitored; want 450 parts and none monitored", len(parts), monitored)
	}
}
