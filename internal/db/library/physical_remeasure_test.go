package library_test

import (
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// A file edited in place keeps its row, and its recorded size and hash follow it.
func TestRegisterBatch_AKnownPathChangedInPlaceIsRemeasured(t *testing.T) {
	opened, _ := newLibrary(t)
	physical := library.NewPhysical(opened)
	at := time.Unix(1_700_100_000, 0).UTC()
	file := library.PhysicalFile{RelPath: "A/01.flac", SizeBytes: 7, QuickHash: "before", FirstSeenAt: at}
	first, err := physical.RegisterBatch(t.Context(), 1, []library.PhysicalFile{file})
	if err != nil {
		t.Fatalf("RegisterBatch: %v", err)
	}
	if first[0].ContentChanged {
		t.Error("a new path reported ContentChanged")
	}

	file.SizeBytes, file.QuickHash = 9, "after"
	second, err := physical.RegisterBatch(t.Context(), 1, []library.PhysicalFile{file})
	if err != nil {
		t.Fatalf("RegisterBatch (changed): %v", err)
	}
	if second[0].MediaObjectID != first[0].MediaObjectID || !second[0].ContentChanged {
		t.Fatalf("second = %+v, want the same object, ContentChanged", second[0])
	}
	object, err := librarydb.New(opened.Read()).GetMediaObject(t.Context(), first[0].MediaObjectID)
	if err != nil {
		t.Fatalf("GetMediaObject: %v", err)
	}
	if object.SizeBytes != 9 || object.QuickHash != "after" {
		t.Errorf("object = size %d hash %q, want 9 / after", object.SizeBytes, object.QuickHash)
	}

	third, err := physical.RegisterBatch(t.Context(), 1, []library.PhysicalFile{file})
	if err != nil || third[0].ContentChanged {
		t.Errorf("an unchanged path = %+v, %v; want no ContentChanged", third[0], err)
	}
}

// A path that shares its object with another no longer holds that object's bytes once they change.
func TestRegisterBatch_ASharedPathChangedInPlaceLeavesTheSharedObject(t *testing.T) {
	opened, _ := newLibrary(t)
	physical := library.NewPhysical(opened)
	at := time.Unix(1_700_100_000, 0).UTC()
	files := []library.PhysicalFile{
		{RelPath: "A/01.flac", SizeBytes: 7, QuickHash: "same", FirstSeenAt: at},
		{RelPath: "B/01.flac", SizeBytes: 7, QuickHash: "same", FirstSeenAt: at},
	}
	ids, err := physical.RegisterBatch(t.Context(), 1, files)
	if err != nil {
		t.Fatalf("RegisterBatch: %v", err)
	}
	files[1].SizeBytes, files[1].QuickHash = 8, "edited"
	after, err := physical.RegisterBatch(t.Context(), 1, files[1:])
	if err != nil {
		t.Fatalf("RegisterBatch (edited): %v", err)
	}
	if after[0].MediaObjectID == ids[0].MediaObjectID || !after[0].ContentChanged {
		t.Fatalf("edited path = %+v, want its own object", after[0])
	}
	shared, err := physical.PathsForObject(t.Context(), ids[0].MediaObjectID)
	if err != nil || len(shared) != 1 || shared[0].RelPath != "A/01.flac" {
		t.Errorf("shared object paths = %+v, %v; want only A/01.flac", shared, err)
	}
}

// The head and tail sample can miss a change in the middle, which still moves the size.
func TestRegisterBatch_ASizeChangeAloneIsRemeasured(t *testing.T) {
	opened, _ := newLibrary(t)
	physical := library.NewPhysical(opened)
	file := library.PhysicalFile{RelPath: "A/01.flac", SizeBytes: 7, QuickHash: "sampled", FirstSeenAt: time.Unix(1_700_100_000, 0).UTC()}
	first, err := physical.RegisterBatch(t.Context(), 1, []library.PhysicalFile{file})
	if err != nil {
		t.Fatalf("RegisterBatch: %v", err)
	}
	file.SizeBytes = 12
	second, err := physical.RegisterBatch(t.Context(), 1, []library.PhysicalFile{file})
	if err != nil || !second[0].ContentChanged {
		t.Fatalf("second = %+v, %v; want ContentChanged", second[0], err)
	}
	object, _ := librarydb.New(opened.Read()).GetMediaObject(t.Context(), first[0].MediaObjectID)
	if object.SizeBytes != 12 {
		t.Errorf("size = %d, want 12", object.SizeBytes)
	}
}
