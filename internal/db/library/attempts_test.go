package library_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
)

func registerOne(t *testing.T, physical library.Physical, relPath string) int64 {
	t.Helper()
	registered, err := physical.RegisterBatch(t.Context(), 1, []library.PhysicalFile{
		{RelPath: relPath, SizeBytes: 1, QuickHash: "hash-" + relPath, FirstSeenAt: time.Unix(1, 0)},
	})
	if err != nil {
		t.Fatalf("RegisterBatch: %v", err)
	}
	return registered[0].MediaObjectID
}

func TestAttempts_RoundTripNewestFirst(t *testing.T) {
	opened, _ := newLibrary(t)
	attempts := library.NewAttempts(opened)
	id := registerOne(t, library.NewPhysical(opened), "P!po/Kebilegev Bibihi/01.m4a")

	older := library.Attempt{MediaObjectID: id, At: 100, Outcome: "retry", Passes: []library.AttemptPass{
		{Pass: "album_match", Outcome: "failed"},
	}}
	newer := library.Attempt{MediaObjectID: id, At: 200, Outcome: "queued", Passes: []library.AttemptPass{
		{Pass: "embedded_tags", Outcome: "none"},
		{Pass: "album_match", Outcome: "ambiguous", Candidates: []library.AttemptCandidate{{
			Strategy: "parse", Provider: "musicbrainz:release-group", ExternalID: "rg-1", Confidence: 0.3,
			Evidence: []library.AttemptEvidence{{Kind: "title", Detail: "Kebilegev Bibihi"}},
		}}},
	}}
	if err := attempts.Record(t.Context(), []library.Attempt{older}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := attempts.Record(t.Context(), []library.Attempt{newer}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	got, err := attempts.List(t.Context(), id)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if want := []library.Attempt{newer, older}; !reflect.DeepEqual(got, want) {
		t.Errorf("List = %+v\nwant %+v", got, want)
	}
}

// A file rescanned forever must not grow its record forever.
func TestAttempts_KeepOnlyTheNewestFivePerFile(t *testing.T) {
	opened, _ := newLibrary(t)
	attempts := library.NewAttempts(opened)
	physical := library.NewPhysical(opened)
	id, other := registerOne(t, physical, "a/1.m4a"), registerOne(t, physical, "b/1.m4a")

	if err := attempts.Record(t.Context(), []library.Attempt{{MediaObjectID: other, At: 1, Outcome: "queued"}}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	for at := int64(1); at <= 7; at++ {
		if err := attempts.Record(t.Context(), []library.Attempt{{MediaObjectID: id, At: at, Outcome: "queued"}}); err != nil {
			t.Fatalf("Record %d: %v", at, err)
		}
	}

	got, err := attempts.List(t.Context(), id)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var times []int64
	for _, attempt := range got {
		times = append(times, attempt.At)
	}
	if want := []int64{7, 6, 5, 4, 3}; !reflect.DeepEqual(times, want) {
		t.Errorf("kept attempts at %v, want %v", times, want)
	}
	if kept, err := attempts.List(t.Context(), other); err != nil || len(kept) != 1 {
		t.Errorf("the other file's record = %d attempts (err %v), want its one untouched", len(kept), err)
	}
}

// D12: once an AcoustID key is entered, the files skipped for the want of one
// have to be findable again. Only a queued file whose *newest* run recorded that
// pass counts -- a file already identified, or one whose later run did run the
// pass, must not come back.
func TestAttempts_ListSkippedAnswersOnlyWhatTheNewestRunSkipped(t *testing.T) {
	opened, _ := newLibrary(t)
	attempts := library.NewAttempts(opened)
	physical := library.NewPhysical(opened)
	queue := library.NewUnidentified(opened)

	skipped := registerOne(t, physical, "Heka/Ripped CD01/01.flac")
	ranIt := registerOne(t, physical, "Heka/Ripped CD01/02.flac")
	notQueued := registerOne(t, physical, "Heka/Ripped CD02/01.flac")

	// Only the first two are still waiting for a human.
	if err := queue.Record(t.Context(), []library.QueueEntry{
		{MediaObjectID: skipped, RootFolderID: 1, RelPath: "Heka/Ripped CD01/01.flac", At: 10},
		{MediaObjectID: ranIt, RootFolderID: 1, RelPath: "Heka/Ripped CD01/02.flac", At: 10},
	}); err != nil {
		t.Fatalf("Record queue: %v", err)
	}

	disabled := library.AttemptPass{Pass: "fingerprint", Outcome: "disabled_no_key"}
	ran := library.AttemptPass{Pass: "fingerprint", Outcome: "none"}
	if err := attempts.Record(t.Context(), []library.Attempt{
		{MediaObjectID: skipped, At: 100, Outcome: "queued", Passes: []library.AttemptPass{disabled}},
		// An older run skipped it, a newer one did not: the newest is what counts.
		{MediaObjectID: ranIt, At: 100, Outcome: "queued", Passes: []library.AttemptPass{disabled}},
		{MediaObjectID: notQueued, At: 100, Outcome: "queued", Passes: []library.AttemptPass{disabled}},
	}); err != nil {
		t.Fatalf("Record attempts: %v", err)
	}
	if err := attempts.Record(t.Context(), []library.Attempt{
		{MediaObjectID: ranIt, At: 200, Outcome: "queued", Passes: []library.AttemptPass{ran}},
	}); err != nil {
		t.Fatalf("Record newer attempt: %v", err)
	}

	got, lastID, err := attempts.ListSkipped(t.Context(), "fingerprint", "disabled_no_key", 0, 100)
	if err != nil {
		t.Fatalf("ListSkipped: %v", err)
	}
	want := []library.SkippedFile{{MediaObjectID: skipped, RootFolderID: 1, Directory: "Heka/Ripped CD01"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ListSkipped = %+v\nwant %+v", got, want)
	}
	// The cursor advances past every row read, not only the ones kept, or the
	// next page would re-read the skipped-over rows forever.
	if lastID != ranIt {
		t.Errorf("lastID = %d, want %d (the last row read)", lastID, ranIt)
	}
}

func TestAttempts_ListSkippedPagesOnTheCursor(t *testing.T) {
	opened, _ := newLibrary(t)
	attempts := library.NewAttempts(opened)
	physical := library.NewPhysical(opened)
	queue := library.NewUnidentified(opened)

	disabled := library.AttemptPass{Pass: "fingerprint", Outcome: "disabled_no_key"}
	var ids []int64
	for i := range 3 {
		relPath := "Heka/Album/0" + string(rune('1'+i)) + ".flac"
		id := registerOne(t, physical, relPath)
		ids = append(ids, id)
		if err := queue.Record(t.Context(), []library.QueueEntry{
			{MediaObjectID: id, RootFolderID: 1, RelPath: relPath, At: 10},
		}); err != nil {
			t.Fatalf("Record queue: %v", err)
		}
		if err := attempts.Record(t.Context(), []library.Attempt{
			{MediaObjectID: id, At: 100, Outcome: "queued", Passes: []library.AttemptPass{disabled}},
		}); err != nil {
			t.Fatalf("Record attempts: %v", err)
		}
	}

	first, lastID, err := attempts.ListSkipped(t.Context(), "fingerprint", "disabled_no_key", 0, 2)
	if err != nil {
		t.Fatalf("ListSkipped page 1: %v", err)
	}
	if len(first) != 2 || lastID != ids[1] {
		t.Fatalf("page 1 = %d rows, lastID %d; want 2 rows ending at %d", len(first), lastID, ids[1])
	}
	second, _, err := attempts.ListSkipped(t.Context(), "fingerprint", "disabled_no_key", lastID, 2)
	if err != nil {
		t.Fatalf("ListSkipped page 2: %v", err)
	}
	if len(second) != 1 || second[0].MediaObjectID != ids[2] {
		t.Errorf("page 2 = %+v, want only media object %d", second, ids[2])
	}
}
