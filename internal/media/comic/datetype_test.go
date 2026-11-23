package comic

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/media/works"
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

func TestAnImplausibleArchiveSizeIsRejectedWithItsReason(t *testing.T) {
	for _, c := range []struct {
		name  string
		size  int64
		parts int
		field bool
	}{{"stub", 4 << 10, 1, true}, {"bundle", 3 << 30, 1, true}, {"range of stubs", 5 << 20, 10, true}, {"normal", 40 << 20, 1, false}, {"unknown size", 0, 1, false}} {
		decision := model.Decision{Outcome: model.OutcomeAccept}
		requirePlausibleArchiveSize(&decision, c.size, c.parts)
		if rejected := decision.Outcome == model.OutcomeReject; rejected != c.field || rejected && decision.Rejections[0].Rule != works.RuleImplausibleSize {
			t.Errorf("%s: outcome %v, rejections %+v", c.name, decision.Outcome, decision.Rejections)
		}
	}
}
