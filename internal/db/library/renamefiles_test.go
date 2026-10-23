package library_test

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

// A rename lists each primary file on the root with the parts of the library it covers: a double episode's two parts
// together, another library's coverage left out, a work filter honoured.
func TestRenameFilesOnRoot_ListsEachFileWithItsParts(t *testing.T) {
	opened, _ := newLibrary(t)
	write := sampleSeries()
	write.Parts = []library.SeriesPartWrite{{MediaRef: "s01:e01", SortKey: "a"}, {MediaRef: "s01:e02", SortKey: "b"}, {MediaRef: "s01:e03", SortKey: "c"}}
	series := library.NewSeries(opened)
	result, err := series.Upsert(t.Context(), seriesAt, write)
	if err != nil {
		t.Fatal(err)
	}
	desired := library.NewDesired(opened)
	if _, err := desired.EnsureSeasonItem(t.Context(), library.SeasonRequest{WorkID: result.WorkID, MediaRef: "s01", CategoryID: 1, ProfileID: 1, Monitored: true}); err != nil {
		t.Fatal(err)
	}
	physical := library.NewPhysical(opened)
	registered, err := physical.RegisterBatch(t.Context(), 1, []library.PhysicalFile{
		{RelPath: "Show/S01E01-E02.mkv", SizeBytes: 5, QuickHash: "a", FirstSeenAt: seriesAt},
		{RelPath: "Show/S01E03.mkv", SizeBytes: 7, QuickHash: "b", FirstSeenAt: seriesAt},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := series.AttachFiles(t.Context(), seriesAt, result.WorkID, 1, []library.SeriesFileLink{
		{MediaObjectID: registered[0].MediaObjectID, Parts: []library.PartKey{{MediaRef: "s01:e01"}, {MediaRef: "s01:e02"}}},
		{MediaObjectID: registered[1].MediaObjectID, Parts: []library.PartKey{{MediaRef: "s01:e03"}}},
	}); err != nil {
		t.Fatal(err)
	}

	got, err := physical.RenameFilesOnRoot(t.Context(), 1, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []library.RenameFile{
		{FileOnRoot: library.FileOnRoot{MediaObjectID: registered[0].MediaObjectID, RelPath: "Show/S01E01-E02.mkv", SizeBytes: 5},
			Works: []library.RenameWork{{WorkID: result.WorkID, MediaRefs: []string{"s01:e01", "s01:e02"}}}},
		{FileOnRoot: library.FileOnRoot{MediaObjectID: registered[1].MediaObjectID, RelPath: "Show/S01E03.mkv", SizeBytes: 7},
			Works: []library.RenameWork{{WorkID: result.WorkID, MediaRefs: []string{"s01:e03"}}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("files = %+v\nwant %+v", got, want)
	}
	if other, err := physical.RenameFilesOnRoot(t.Context(), 2, 1, 0); err != nil || len(other) != 0 {
		t.Errorf("another library's rename = %+v, %v; want none of these", other, err)
	}
	if none, err := physical.RenameFilesOnRoot(t.Context(), 1, 1, result.WorkID+1); err != nil || len(none) != 0 {
		t.Errorf("another work's rename = %+v, %v; want none", none, err)
	}
}
