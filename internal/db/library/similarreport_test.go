package library_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
)

func storedReportFixture(t *testing.T, videos int) (library.SimilarVideos, []int64, func(id int64)) {
	t.Helper()
	opened, _ := newLibrary(t)
	physical := library.NewPhysical(opened)
	files := make([]library.PhysicalFile, videos)
	for index := range files {
		files[index] = library.PhysicalFile{
			RelPath: fmt.Sprintf("Film %03d.mkv", index), SizeBytes: int64(1000 + index),
			QuickHash: fmt.Sprintf("hash-%03d", index), FirstSeenAt: time.Unix(0, 0),
		}
	}
	registered, err := physical.RegisterBatch(t.Context(), 1, files)
	if err != nil {
		t.Fatalf("RegisterBatch: %v", err)
	}
	frames := []library.FrameHash{{Slot: 0, Hash: 1}, {Slot: 1, Hash: 2}, {Slot: 2, Hash: 3}, {Slot: 3, Hash: 4}, {Slot: 4, Hash: 5}}
	var ids []int64
	for _, file := range registered {
		if _, err := opened.WriteDB().ExecContext(t.Context(), `UPDATE media_object SET kind = 'video' WHERE id = ?`, file.MediaObjectID); err != nil {
			t.Fatalf("mark video: %v", err)
		}
		if err := physical.SetPerceptualHash(t.Context(), file.MediaObjectID, 7, frames); err != nil {
			t.Fatalf("SetPerceptualHash: %v", err)
		}
		ids = append(ids, file.MediaObjectID)
	}
	gone := func(id int64) {
		if _, err := opened.WriteDB().ExecContext(t.Context(), `UPDATE library_path SET missing_since = 5 WHERE media_object_id = ?`, id); err != nil {
			t.Fatalf("mark path missing: %v", err)
		}
	}
	return library.NewSimilarVideos(opened), ids, gone
}

func TestSimilarReport_StoredIsWhatTheJobComputedMinusWhatLeftSince(t *testing.T) {
	similar, ids, gone := storedReportFixture(t, 4)
	empty, err := similar.Stored(t.Context(), []int64{1}, library.SimilarityMedium, 10)
	if err != nil || empty.ComputedAt != 0 || len(empty.Groups) != 0 {
		t.Fatalf("before any computation: %+v, %v; want nothing", empty, err)
	}
	if err := similar.Refresh(t.Context(), []int64{1}, library.SimilarityMedium, 1234); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	report, err := similar.Stored(t.Context(), []int64{1}, library.SimilarityMedium, 10)
	if err != nil {
		t.Fatalf("Stored: %v", err)
	}
	if report.ComputedAt != 1234 || len(report.Groups) != 1 || len(report.Groups[0].Videos) != 4 || report.Groups[0].Videos[0].File.SizeBytes == 0 {
		t.Fatalf("report = %+v, want one group of four with facts, computed at 1234", report)
	}
	if other, _ := similar.Stored(t.Context(), []int64{1}, library.SimilarityLow, 10); other.ComputedAt != 0 {
		t.Fatalf("another level's report = %+v, want none", other)
	}
	if elsewhere, _ := similar.Stored(t.Context(), []int64{2}, library.SimilarityMedium, 10); len(elsewhere.Groups) != 0 {
		t.Fatalf("a library without these videos got %+v", elsewhere.Groups)
	}
	gone(ids[0])
	report, _ = similar.Stored(t.Context(), []int64{1}, library.SimilarityMedium, 10)
	if len(report.Groups) != 1 || len(report.Groups[0].Videos) != 3 {
		t.Fatalf("after one file left: %+v, want a group of three", report.Groups)
	}
	gone(ids[1])
	gone(ids[2])
	report, _ = similar.Stored(t.Context(), []int64{1}, library.SimilarityMedium, 10)
	if len(report.Groups) != 0 {
		t.Fatalf("a single video left still shown as a group: %+v", report.Groups)
	}
}
