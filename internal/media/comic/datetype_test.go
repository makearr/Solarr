package comic

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

func TestPartWriteDateType(t *testing.T) {
	part := Part{Kind: PartIssue, Number: "7", CoverDate: "2031-05-00", Store: "2031-03-12"}
	if got := partWrite(part, false, false); got.Date != "2031-03-12" || got.AvailableBasis != "store date" {
		t.Fatalf("store date type: %q %q", got.Date, got.AvailableBasis)
	}
	if got := partWrite(part, false, true); got.Date != "2031-05-01" || got.AvailableBasis != "cover date" {
		t.Fatalf("cover date type: %q %q", got.Date, got.AvailableBasis)
	}
	if got := partWrite(Part{Kind: PartIssue, Number: "8", Store: "2031-04-02"}, false, true); got.Date != "2031-04-02" {
		t.Fatalf("a part without a cover date keeps its store date: %q", got.Date)
	}
}

func TestAnEditionSatisfiesItsIssuesOnlyWithTheSettingOn(t *testing.T) {
	covered := library.SeriesEpisode{MediaRef: "issue:1", InEdition: &library.EditionRef{WorkID: 9, Title: "Book One"}}
	if !standsInEdition(true, covered) || standsInEdition(false, covered) || standsInEdition(true, library.SeriesEpisode{MediaRef: "issue:2"}) {
		t.Fatal("only a covered part with the setting on is left out of the search")
	}
}
