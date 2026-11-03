package judge_test

import (
	"encoding/json"
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/identify/score"
)

func qualityOf(rank int, label string) model.Decision {
	return model.Decision{Quality: model.QualityVector{Tiers: []model.TierValue{{Dimension: "lossless", Rank: rank, Label: label}}}}
}

func TestDescribeReplacement_NamesTheTierThatDecided(t *testing.T) {
	won := judge.DescribeReplacement(5, 11, 10, qualityOf(0, "lossless"), qualityOf(1, "lossy"), score.Facts{})
	if won.Outcome != library.ImportDecisionImport || won.IncumbentObject != 10 || won.DesiredItemID != 5 {
		t.Errorf("won = %+v, want an IMPORT of item 5 over object 10", won)
	}
	var reason judge.Reason
	if err := json.Unmarshal([]byte(won.Reason), &reason); err != nil || reason != (judge.Reason{DecidedBy: "lossless", Winner: "arrived"}) {
		t.Errorf("reason = %+v, %v; want lossless decided for the arrival", reason, err)
	}
	var comparison judge.Comparison
	if err := json.Unmarshal([]byte(won.Comparison), &comparison); err != nil || comparison.Arrived.MediaObjectID != 11 || comparison.Incumbent.Tiers[0].Label != "lossy" {
		t.Errorf("comparison = %+v, %v; want both sides with their tiers", comparison, err)
	}
}

func TestDescribeReplacement_ATieKeepsTheIncumbent(t *testing.T) {
	tied := judge.DescribeReplacement(5, 11, 10, qualityOf(0, "lossless"), qualityOf(0, "lossless"), score.Facts{})
	if tied.Outcome != library.ImportDecisionRejectWorse {
		t.Errorf("outcome = %q, want REJECT_WORSE: a tie keeps the owned file", tied.Outcome)
	}
	var reason judge.Reason
	if err := json.Unmarshal([]byte(tied.Reason), &reason); err != nil || reason != (judge.Reason{DecidedBy: "score", Winner: "incumbent"}) {
		t.Errorf("reason = %+v, %v; want the score, incumbent kept", reason, err)
	}
}
