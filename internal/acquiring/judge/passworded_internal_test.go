package judge

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/identify/model"
)

// An indexer saying a release needs a password rejects it, whatever else was decided (IDX-1).
func TestRejectPasswordedTurnsAnAcceptIntoARejection(t *testing.T) {
	finds := []Find{
		{Raw: indexer.RawItem{GUID: "plain"}, Decision: model.Decision{Outcome: model.OutcomeAccept}},
		{Raw: indexer.RawItem{GUID: "locked", Passworded: true}, Decision: model.Decision{Outcome: model.OutcomeAccept}},
	}
	rejectPassworded(finds)
	if finds[0].Decision.Outcome != model.OutcomeAccept {
		t.Errorf("plain release = %v, want ACCEPT", finds[0].Decision.Outcome)
	}
	want := model.Decision{
		Outcome:    model.OutcomeReject,
		Gates:      []model.GateResult{{Gate: "passworded", Wanted: "not passworded", Got: "passworded"}},
		Rejections: []model.Rejection{{Rule: "passworded", Field: "passworded", Wanted: "not passworded", Got: "passworded"}},
	}
	if !reflect.DeepEqual(finds[1].Decision, want) {
		t.Errorf("locked release decision = %+v, want %+v", finds[1].Decision, want)
	}
}
