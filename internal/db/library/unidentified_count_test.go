package library_test

import (
	"testing"

	"github.com/makearr/MediaNexus/internal/db/library"
)

// Library health's own "unmatched files" figure (todo 4.8).
func TestUnidentifiedCount(t *testing.T) {
	opened, _ := newLibrary(t)
	unidentified := library.NewUnidentified(opened)

	zero, err := unidentified.Count(t.Context())
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if zero != 0 {
		t.Errorf("Count = %d, want 0 before anything is queued", zero)
	}

	object1 := createObject(t, opened, "unidentified-quick-1", 100)
	object2 := createObject(t, opened, "unidentified-quick-2", 200)
	if err := unidentified.Record(t.Context(), []library.QueueEntry{
		{MediaObjectID: object1.ID, RootFolderID: 1, RelPath: "a.flac", At: 1},
		{MediaObjectID: object2.ID, RootFolderID: 1, RelPath: "b.flac", At: 1},
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	got, err := unidentified.Count(t.Context())
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if got != 2 {
		t.Errorf("Count = %d, want 2", got)
	}

	if err := unidentified.Clear(t.Context(), []int64{object1.ID}); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	got, err = unidentified.Count(t.Context())
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if got != 1 {
		t.Errorf("Count after clearing one = %d, want 1", got)
	}
}
