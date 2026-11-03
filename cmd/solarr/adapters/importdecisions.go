package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
)

// importDecisionsShown bounds one answer; a work rarely has more replacements than that.
const importDecisionsShown = 50

// ImportDecisions answers a work's recorded replacements for the web interface, decoding what the judge stored.
type ImportDecisions struct{ Decisions library.ImportDecisions }

// ForWork lists the work's replacement decisions, newest first.
func (a ImportDecisions) ForWork(ctx context.Context, workID int64) ([]apiserver.ImportDecisionView, error) {
	rows, err := a.Decisions.ForWork(ctx, workID, importDecisionsShown)
	if err != nil {
		return nil, err
	}
	views := make([]apiserver.ImportDecisionView, 0, len(rows))
	for _, row := range rows {
		var comparison judge.Comparison
		if err := json.Unmarshal([]byte(row.Comparison), &comparison); err != nil {
			return nil, fmt.Errorf("import decision %d: comparison: %w", row.ID, err)
		}
		var reason judge.Reason
		if err := json.Unmarshal([]byte(row.Reason), &reason); err != nil {
			return nil, fmt.Errorf("import decision %d: reason: %w", row.ID, err)
		}
		views = append(views, apiserver.ImportDecisionView{
			ID: row.ID, Outcome: row.Outcome, DecidedAt: time.Unix(row.DecidedAt, 0), DecidedBy: reason.DecidedBy, Winner: reason.Winner,
			Arrived:   apiserver.ReplacementSideView{MediaObjectID: comparison.Arrived.MediaObjectID, Tiers: comparison.Arrived.Tiers, Score: comparison.Arrived.Score},
			Incumbent: apiserver.ReplacementSideView{MediaObjectID: comparison.Incumbent.MediaObjectID, Tiers: comparison.Incumbent.Tiers, Score: comparison.Incumbent.Score},
		})
	}
	return views, nil
}
