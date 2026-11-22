package comic

import (
	"reflect"
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/importing/inspect"
)

func TestFilesOfADownloadSortAsNumbersAndSkipOtherFiles(t *testing.T) {
	unit := inspect.Unit{Media: []inspect.File{{RelPath: "Night Harbor 10.cbz"}, {RelPath: "Night Harbor 2.cbz"}, {RelPath: "cover.jpg"}, {RelPath: "._Night Harbor 3.cbz"}}}
	var got []string
	for _, file := range comicFiles(unit) {
		got = append(got, file.RelPath)
	}
	if want := []string{"Night Harbor 2.cbz", "Night Harbor 10.cbz"}; !slices.Equal(got, want) {
		t.Fatalf("files = %v, want %v", got, want)
	}
}

func TestAFileIsNamedByItsFirstPartAndASlotOfItsKind(t *testing.T) {
	detail := library.SeriesDetailRow{Summary: library.SeriesSummary{Title: "Night Harbor", Year: 2019, Fields: map[string]string{"publisher": "Acme Press"}}, IDs: map[string]string{}}
	cases := []struct {
		name    string
		keys    []library.PartKey
		slot    string
		issue   string
		volume  int
		chapter string
		format  string
	}{
		{name: "issue", keys: []library.PartKey{{MediaRef: "issue:3"}}, slot: SlotIssueFile, issue: "3"},
		{name: "range", keys: []library.PartKey{{MediaRef: "issue:1"}, {MediaRef: "issue:2"}, {MediaRef: "issue:3"}}, slot: SlotIssueFile, issue: "1-3"},
		{name: "annual", keys: []library.PartKey{{MediaRef: "annual:1"}}, slot: SlotIssueFile, issue: "1", format: "annual"},
		{name: "volume", keys: []library.PartKey{{MediaRef: "volume:4"}}, slot: SlotVolumeFile, volume: 4},
		{name: "chapter", keys: []library.PartKey{{MediaRef: "chapter:12.5"}}, slot: SlotChapterFile, chapter: "12.5"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			slot, got := namingOf(detail, importFile{file: inspect.File{RelPath: "night.cbz"}, keys: c.keys})
			want := FileNaming{Series: "Night Harbor", Year: 2019, Publisher: "Acme Press", Issue: c.issue, Volume: c.volume, Chapter: c.chapter, Format: c.format, OriginalFilename: "night"}
			if slot != c.slot || !reflect.DeepEqual(got, want) {
				t.Fatalf("namingOf = %q %+v, want %q %+v", slot, got, c.slot, want)
			}
		})
	}
}
