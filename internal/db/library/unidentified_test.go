package library_test

import (
	"slices"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
)

// "Not media" takes a file out of the queue, and a later scan recording it again does not bring it back.
func TestUnidentified_NotMediaStaysOutOfTheQueue(t *testing.T) {
	opened, _ := newLibrary(t)
	physical := library.NewPhysical(opened)
	queue := library.NewUnidentified(opened)
	cover := registerOne(t, physical, "Heka/Zanuvode/scan.flac")
	entry := library.QueueEntry{MediaObjectID: cover, RootFolderID: 1, RelPath: "Heka/Zanuvode/scan.flac", At: 10}
	if err := queue.Record(t.Context(), []library.QueueEntry{entry}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	if err := queue.MarkNotMedia(t.Context(), cover, 20); err != nil {
		t.Fatalf("MarkNotMedia: %v", err)
	}
	entry.At = 30
	if err := queue.Record(t.Context(), []library.QueueEntry{entry}); err != nil {
		t.Fatalf("Record again: %v", err)
	}
	if count, err := queue.Count(t.Context()); err != nil || count != 0 {
		t.Errorf("queued %d, %v; want the not-media file kept out", count, err)
	}
}

// "Identify the whole folder as": the queued files directly beside one, not in a sub-folder or a sibling.
func TestUnidentified_InFolderIsOneFolderDeep(t *testing.T) {
	opened, _ := newLibrary(t)
	physical := library.NewPhysical(opened)
	queue := library.NewUnidentified(opened)
	var entries []library.QueueEntry
	ids := map[string]int64{}
	for _, relPath := range []string{
		"Heka/Zanuvode/01.flac", "Heka/Zanuvode/02.flac", "Heka/Zanuvode/CD2/01.flac",
		"Heka/Zanuvode (Deluxe)/01.flac", "loose.flac",
	} {
		ids[relPath] = registerOne(t, physical, relPath)
		entries = append(entries, library.QueueEntry{MediaObjectID: ids[relPath], RootFolderID: 1, RelPath: relPath, At: 10})
	}
	if err := queue.Record(t.Context(), entries); err != nil {
		t.Fatalf("Record: %v", err)
	}

	got, err := queue.InFolder(t.Context(), 1, "Heka/Zanuvode/02.flac")
	if want := []int64{ids["Heka/Zanuvode/01.flac"], ids["Heka/Zanuvode/02.flac"]}; err != nil || !slices.Equal(got, want) {
		t.Errorf("InFolder = %v, %v; want %v", got, err, want)
	}
	got, err = queue.InFolder(t.Context(), 1, "loose.flac")
	if want := []int64{ids["loose.flac"]}; err != nil || !slices.Equal(got, want) {
		t.Errorf("InFolder(root) = %v, %v; want %v", got, err, want)
	}
}

// A person's resolve survives a full rescan: a file already belonging to a work is never queued again.
func TestUnidentified_AFileBelongingToAWorkIsNeverQueued(t *testing.T) {
	opened, _ := newLibrary(t)
	physical := library.NewPhysical(opened)
	queue := library.NewUnidentified(opened)
	resolved := registerOne(t, physical, "Heka/Zanuvode/01.flac")
	work := createWork(t, opened, "music", "Zanuvode")
	if _, err := library.NewIdentity(opened).ResolveWorks(t.Context(), time.Unix(10, 0), []library.WorkLink{{
		Provider: "musicbrainz:release-group", ExternalID: "rg-1", Confidence: 1,
		Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: work.CanonicalTitle}, RootFolderID: 1,
		MediaObjectID: resolved,
	}}); err != nil {
		t.Fatalf("ResolveWorks: %v", err)
	}
	if err := queue.Record(t.Context(), []library.QueueEntry{
		{MediaObjectID: resolved, RootFolderID: 1, RelPath: "Heka/Zanuvode/01.flac", At: 20},
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if count, err := queue.Count(t.Context()); err != nil || count != 0 {
		t.Errorf("queued %d, %v; want the resolved file left alone", count, err)
	}
}
