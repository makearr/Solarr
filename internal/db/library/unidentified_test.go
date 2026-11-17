package library_test

import (
	"encoding/json"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/platform/paginate"
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

// The import job stores identifications whole and a type's own importer the compact form; both read back alike.
func TestUnitCandidates_ReadsBothStoredForms(t *testing.T) {
	guess := model.Identification{
		Strategy: "parse", Confidence: 0.6,
		ExternalID: &model.ExternalID{Provider: "tvdb", ExternalID: "77", Confidence: 0.6},
		Evidence:   []model.MatchEvidence{{Kind: "parse", Detail: "Marble Hill"}},
	}
	whole, err := json.Marshal([]model.Identification{guess})
	if err != nil {
		t.Fatal(err)
	}
	for name, stored := range map[string]string{"whole": string(whole), "compact": library.CandidatesJSON([]model.Identification{guess})} {
		read, err := library.UnitCandidates(stored)
		if err != nil || len(read) != 1 || read[0].ExternalID == nil || *read[0].ExternalID != *guess.ExternalID ||
			len(read[0].Evidence) != 1 || read[0].Evidence[0] != guess.Evidence[0] || read[0].Confidence != 0.6 {
			t.Errorf("%s: read %+v, %v; want the guess with its id and title", name, read, err)
		}
	}
	if none, err := library.UnitCandidates(""); err != nil || none != nil {
		t.Errorf("nothing stored: %+v, %v; want none", none, err)
	}
}

// Seen again with the same answer for the same bytes, a file's automatic retry moves out a day, then two, four, eight; a
// changed file or a different answer starts over.
func TestUnidentified_ARepeatedAnswerBacksTheRetryOffAndAChangeResetsIt(t *testing.T) {
	opened, _ := newLibrary(t)
	physical := library.NewPhysical(opened)
	queue := library.NewUnidentified(opened)
	id := registerOne(t, physical, "Heka/Zanuvode/01.flac")
	const day = int64(86400)
	entry := library.QueueEntry{MediaObjectID: id, RootFolderID: 1, RelPath: "Heka/Zanuvode/01.flac", At: 1000}
	record := func(at int64) library.Row {
		t.Helper()
		entry.At = at
		if err := queue.Record(t.Context(), []library.QueueEntry{entry}); err != nil {
			t.Fatalf("Record: %v", err)
		}
		row, err := queue.Get(t.Context(), id)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		return row
	}

	if row := record(1000); row.Tries != 0 || row.NextTryAt != 0 {
		t.Fatalf("first sighting: tries %d next %d, want 0 and due now", row.Tries, row.NextTryAt)
	}
	at := int64(2000)
	for tries, wait := range []int64{day, 2 * day, 4 * day, 8 * day} {
		if row := record(at); row.Tries != int64(tries+1) || row.NextTryAt != at+wait {
			t.Fatalf("repeat %d: tries %d next %d, want %d and %d", tries+1, row.Tries, row.NextTryAt, tries+1, at+wait)
		}
		at += 10
	}
	entry.Candidates = []model.Identification{{Strategy: "tags", Confidence: 0.5}}
	if row := record(at); row.Tries != 0 || row.NextTryAt != 0 {
		t.Fatalf("a different answer: tries %d next %d, want a fresh start", row.Tries, row.NextTryAt)
	}
}

func TestUnidentified_ListDueSkipsFilesWhoseRetryIsNotDue(t *testing.T) {
	opened, _ := newLibrary(t)
	physical := library.NewPhysical(opened)
	queue := library.NewUnidentified(opened)
	fresh := registerOne(t, physical, "A/01.flac")
	tried := registerOne(t, physical, "B/01.flac")
	entries := []library.QueueEntry{
		{MediaObjectID: fresh, RootFolderID: 1, RelPath: "A/01.flac", At: 10},
		{MediaObjectID: tried, RootFolderID: 1, RelPath: "B/01.flac", At: 10},
	}
	for _, at := range []int64{10, 20} {
		entries[1].At = at
		if err := queue.Record(t.Context(), entries[1:]); err != nil {
			t.Fatal(err)
		}
	}
	if err := queue.Record(t.Context(), entries[:1]); err != nil {
		t.Fatal(err)
	}
	due, err := queue.ListDue(t.Context(), 1, 30, paginate.Cursor{}, 10)
	if err != nil || len(due) != 1 || due[0].MediaObjectID != fresh {
		t.Fatalf("due = %+v, %v; want only the file never retried", due, err)
	}
	all, err := queue.ListDue(t.Context(), 1, math.MaxInt64, paginate.Cursor{}, 10)
	if err != nil || len(all) != 2 {
		t.Fatalf("forced = %+v, %v; want both", all, err)
	}
}
