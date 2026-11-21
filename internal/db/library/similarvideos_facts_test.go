package library_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
)

func TestSimilarVideos_GroupsCarryTheFactsOfEveryVideoPastOneChunk(t *testing.T) {
	opened, _ := newLibrary(t)
	physical := library.NewPhysical(opened)
	const videos = 205
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
	for _, file := range registered {
		if _, err := opened.WriteDB().ExecContext(t.Context(), `UPDATE media_object SET kind = 'video' WHERE id = ?`, file.MediaObjectID); err != nil {
			t.Fatalf("mark video: %v", err)
		}
		if err := physical.SetPerceptualHash(t.Context(), file.MediaObjectID, 7, frames); err != nil {
			t.Fatalf("SetPerceptualHash: %v", err)
		}
	}

	groups, err := library.NewSimilarVideos(opened).Groups(t.Context(), []int64{1}, library.SimilarityMedium, 10)
	if err != nil {
		t.Fatalf("Groups: %v", err)
	}
	if len(groups) != 1 || len(groups[0].Videos) != videos {
		t.Fatalf("groups = %d, want one group of %d videos", len(groups), videos)
	}
	for _, video := range groups[0].Videos {
		if video.File.MediaObjectID != video.MediaObjectID || video.File.SizeBytes != video.SizeBytes || video.File.SizeBytes == 0 {
			t.Fatalf("video %d: facts %+v, want its own", video.MediaObjectID, video.File)
		}
	}
}
