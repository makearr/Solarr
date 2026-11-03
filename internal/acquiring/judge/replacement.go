package judge

import (
	"encoding/json"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/identify/score"
)

// ReplacementSide is one file in a replacement: where it ranks on each tier and its score.
type ReplacementSide struct {
	MediaObjectID int64             `json:"media_object_id"`
	Tiers         []model.TierValue `json:"tiers"`
	Score         float64           `json:"score"`
}

// Comparison is the side-by-side of two files for one part: what came in against what was owned.
type Comparison struct {
	Arrived   ReplacementSide `json:"arrived"`
	Incumbent ReplacementSide `json:"incumbent"`
}

// Reason says which tier decided, or that the tiers tied and the score did; a tie keeps the incumbent (ADR-40).
type Reason struct {
	DecidedBy string `json:"decided_by"`
	Winner    string `json:"winner"`
}

func side(id int64, decision model.Decision) ReplacementSide {
	return ReplacementSide{MediaObjectID: id, Tiers: decision.Quality.Tiers, Score: decision.Score()}
}

// DescribeReplacement builds the import decision for one settled part: arrived is the downloaded file, incumbent the owned
// one; the store dates it. Outcome is IMPORT when the arrival won and REJECT_WORSE when it did not.
func DescribeReplacement(
	itemID, arrivedID, incumbentID int64, arrived, incumbent model.Decision, arrivedFacts score.Facts,
) library.ImportDecision {
	comparison := Comparison{Arrived: side(arrivedID, arrived), Incumbent: side(incumbentID, incumbent)}
	won := StrictlyBetter(arrived, incumbent)
	_, tier := model.Compare(arrived.Quality, incumbent.Quality)
	reason := Reason{DecidedBy: "score", Winner: "incumbent"}
	if tier >= 0 && tier < len(arrived.Quality.Tiers) {
		reason.DecidedBy = arrived.Quality.Tiers[tier].Dimension
	}
	outcome := library.ImportDecisionRejectWorse
	if won {
		outcome, reason.Winner = library.ImportDecisionImport, "arrived"
	}
	probe, _ := json.Marshal(arrivedFacts.File)
	compared, _ := json.Marshal(comparison)
	why, _ := json.Marshal(reason)
	return library.ImportDecision{
		DesiredItemID: itemID, IncumbentObject: incumbentID, ArrivedProbe: string(probe), Comparison: string(compared),
		Outcome: outcome, Reason: string(why),
	}
}
