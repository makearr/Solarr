package library_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/makearr/MediaNexus/internal/db/library"
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
