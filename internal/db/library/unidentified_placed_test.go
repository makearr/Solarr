package library_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

func TestClearPlacedDropsOnlyTheEntriesWhoseFileIsAttached(t *testing.T) {
	opened, _ := newLibrary(t)
	series := library.NewSeries(opened)
	result, err := series.Upsert(t.Context(), seriesAt, library.SeriesWrite{
		Work:  library.WorkIdentity{MediaType: "tv", CanonicalTitle: "Placed", SortTitle: "placed"},
		IDs:   []library.ExternalIDWrite{{Provider: "tvdb", ExternalID: "55"}},
		Parts: []library.SeriesPartWrite{{MediaRef: "s01:e01", SortKey: "s0001e0001"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := library.NewDesired(opened).EnsureSeasonItem(t.Context(), library.SeasonRequest{WorkID: result.WorkID, MediaRef: "s01", CategoryID: 1, ProfileID: 1}); err != nil {
		t.Fatal(err)
	}
	registered, err := library.NewPhysical(opened).RegisterBatch(t.Context(), 1, []library.PhysicalFile{
		{RelPath: "a/placed.mkv", SizeBytes: 10, QuickHash: "h1", FirstSeenAt: seriesAt}, {RelPath: "a/waiting.mkv", SizeBytes: 11, QuickHash: "h2", FirstSeenAt: seriesAt},
	})
	if err != nil {
		t.Fatal(err)
	}
	queue := library.NewUnidentified(opened)
	if err := queue.Record(t.Context(), []library.QueueEntry{
		{MediaObjectID: registered[0].MediaObjectID, RootFolderID: 1, RelPath: "a/placed.mkv", Candidates: nil, At: 1},
		{MediaObjectID: registered[1].MediaObjectID, RootFolderID: 1, RelPath: "a/waiting.mkv", Candidates: nil, At: 1},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := series.AttachFiles(t.Context(), seriesAt, result.WorkID, 1, []library.SeriesFileLink{
		{MediaObjectID: registered[0].MediaObjectID, Parts: []library.PartKey{{MediaRef: "s01:e01"}}},
	}); err != nil {
		t.Fatal(err)
	}

	if err := queue.ClearPlaced(t.Context()); err != nil {
		t.Fatal(err)
	}
	left, err := librarydb.New(opened.Read()).CountUnidentified(t.Context(), librarydb.CountUnidentifiedParams{FilterRootFolders: 0, RootFolderIds: "[]"})
	if err != nil || left != 1 {
		t.Errorf("queued = %d, err = %v; want only the file nothing has taken", left, err)
	}
	if _, err := queue.Get(t.Context(), registered[1].MediaObjectID); err != nil {
		t.Errorf("the waiting file left the queue: %v", err)
	}
}
