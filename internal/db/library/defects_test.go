package library_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
)

func TestADefectIsRecordedForAnEmptyFileAtOnceAndForAnUnreadableOneOnTheSecondFailedProbe(t *testing.T) {
	opened, _ := newLibrary(t)
	physical := library.NewPhysical(opened)
	defects := library.NewFileDefects(opened)
	empty := registerFileForArtwork(t, physical, 3, "Lantern/empty.mkv", "q-empty")
	unreadable := registerFileForArtwork(t, physical, 3, "Lantern/cut.mkv", "q-cut")
	at := time.Unix(1_700_000_000, 0)

	if err := physical.RecordProbeFailure(t.Context(), empty, 0, "no streams", "", at); err != nil {
		t.Fatal(err)
	}
	if err := physical.RecordProbeFailure(t.Context(), unreadable, 5, "moov atom not found", "", at); err != nil {
		t.Fatal(err)
	}
	first, err := defects.List(t.Context(), []int64{3}, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || first[0].MediaObjectID != empty || first[0].Reason != library.DefectEmpty {
		t.Fatalf("after one failure defects = %+v, want only the empty file", first)
	}
	if err := physical.RecordProbeFailure(t.Context(), unreadable, 5, "moov atom not found", "", at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	second, err := defects.List(t.Context(), []int64{3}, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{library.DefectEmpty, library.DefectUnreadable}
	got := []string{second[0].Reason, second[1].Reason}
	if len(second) != 2 || !reflect.DeepEqual(got, want) || second[1].Detail != "moov atom not found" || second[1].RelPath != "Lantern/cut.mkv" {
		t.Errorf("after two failures defects = %+v, want the empty and the unreadable file", second)
	}
	if total, _ := defects.Count(t.Context(), []int64{3}); total != 2 {
		t.Errorf("count = %d, want 2", total)
	}
	if other, _ := defects.List(t.Context(), []int64{9}, 0, 10); len(other) != 0 {
		t.Errorf("another library's roots list %+v, want none", other)
	}
	kept, err := defects.InRoots(t.Context(), []int64{empty, unreadable, 999}, []int64{3})
	if err != nil || !reflect.DeepEqual(kept, []int64{empty, unreadable}) {
		t.Errorf("InRoots = %v, %v, want both defective files and not the unknown id", kept, err)
	}
	if err := physical.ClearDefect(t.Context(), empty); err != nil {
		t.Fatal(err)
	}
	if total, _ := defects.Count(t.Context(), []int64{3}); total != 1 {
		t.Errorf("count after a clear = %d, want 1", total)
	}
}
