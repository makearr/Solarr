package library_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

func TestAHeldEditionStandsForTheIssuesItCovers(t *testing.T) {
	opened, _ := newLibrary(t)
	series := library.NewSeries(opened)
	issues := library.SeriesWrite{
		Work:  library.WorkIdentity{MediaType: "tv", CanonicalTitle: "Lantern Run", SortTitle: "lantern run"},
		IDs:   []library.ExternalIDWrite{{Provider: "tvdb", ExternalID: "9001"}},
		Parts: []library.SeriesPartWrite{{MediaRef: "issue:1", SortKey: "a", MonitoredIfNew: true}, {MediaRef: "issue:2", SortKey: "b", MonitoredIfNew: true}, {MediaRef: "issue:3", SortKey: "c", MonitoredIfNew: true}},
	}
	edition := library.SeriesWrite{
		Work:  library.WorkIdentity{MediaType: "tv", CanonicalTitle: "Lantern Run, Book One", SortTitle: "lantern run book one"},
		IDs:   []library.ExternalIDWrite{{Provider: "tvdb", ExternalID: "9002"}},
		Parts: []library.SeriesPartWrite{{MediaRef: "whole", SortKey: "a", MonitoredIfNew: true}},
	}
	run, err := series.Upsert(t.Context(), seriesAt, issues)
	if err != nil {
		t.Fatal(err)
	}
	book, err := series.Upsert(t.Context(), seriesAt, edition)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := library.NewDesired(opened).EnsureSeasonItem(t.Context(), library.SeasonRequest{WorkID: run.WorkID, MediaRef: "issue", CategoryID: 1, ProfileID: 1, Monitored: true}); err != nil {
		t.Fatal(err)
	}
	coverage := library.NewCoverage(opened)
	skipped, err := coverage.Replace(t.Context(), book.WorkID, "whole", library.CoverageSourceHand,
		[]library.CoveredPart{{WorkID: run.WorkID, MediaRef: "issue:1"}, {WorkID: run.WorkID, MediaRef: "issue:2"}, {WorkID: run.WorkID, MediaRef: "issue:9"}})
	if err != nil || skipped != 1 {
		t.Fatalf("replace = %d, %v", skipped, err)
	}
	reader := library.NewSeriesReader(opened)
	inEdition := func() []string {
		detail, err := reader.GetOf(t.Context(), "tv", 1, run.WorkID, seriesAt)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, part := range detail.Episodes {
			if part.InEdition != nil {
				got = append(got, part.MediaRef+" in "+part.InEdition.Title)
			}
		}
		return got
	}
	if got := inEdition(); len(got) != 0 {
		t.Fatalf("an edition that is not held stands for nothing: %v", got)
	}
	if _, err := library.NewDesired(opened).EnsureSeasonItem(t.Context(), library.SeasonRequest{WorkID: book.WorkID, MediaRef: "whole", CategoryID: 1, ProfileID: 1, Monitored: true}); err != nil {
		t.Fatal(err)
	}
	registered, err := library.NewPhysical(opened).RegisterBatch(t.Context(), 1, []library.PhysicalFile{{RelPath: "book.cbz", SizeBytes: 1, QuickHash: "h", FirstSeenAt: seriesAt}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := series.AttachFiles(t.Context(), seriesAt, book.WorkID, 1, []library.SeriesFileLink{{MediaObjectID: registered[0].MediaObjectID, Parts: []library.PartKey{{MediaRef: "whole"}}}}); err != nil {
		t.Fatal(err)
	}
	want := []string{"issue:1 in Lantern Run, Book One", "issue:2 in Lantern Run, Book One"}
	if got := inEdition(); len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("held edition covers %v, want %v", got, want)
	}
	rows, err := coverage.OfEdition(t.Context(), book.WorkID)
	if err != nil || len(rows) != 2 || rows[0].CoveredRef != "issue:1" || rows[0].Source != library.CoverageSourceHand {
		t.Fatalf("of edition = %+v, %v", rows, err)
	}
}
