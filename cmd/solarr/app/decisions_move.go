package app

import (
	"context"
	"log/slog"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/statistics"
)

// decisionCopyBatch bounds each transaction of the copy (Invariant 2).
const decisionCopyBatch = 200

// copyDecisions moves library.db's candidates, decisions and aged-decision counts into statistics.db under their
// own ids, so held releases, acquisitions and a person's pick keep naming the same rows. It resumes after the newest
// row already copied, and copies candidates first, since a decision names its candidate.
func (p *process) copyDecisions(ctx context.Context) error {
	from, into := library.NewCandidates(p.library), statistics.NewCandidates(p.statistics)
	afterCandidate, afterDecision, err := into.LastCopied(ctx)
	if err != nil {
		return err
	}
	candidates, decisions := 0, 0
	for {
		batch, err := from.CandidatesAfter(ctx, afterCandidate, decisionCopyBatch)
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			break
		}
		if err := into.CopyCandidates(ctx, batch); err != nil {
			return err
		}
		afterCandidate, candidates = batch[len(batch)-1].ID, candidates+len(batch)
	}
	for {
		batch, err := from.DecisionsAfter(ctx, afterDecision, decisionCopyBatch)
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			break
		}
		if err := into.CopyDecisions(ctx, batch); err != nil {
			return err
		}
		afterDecision, decisions = batch[len(batch)-1].ID, decisions+len(batch)
	}
	rollups, err := from.DecisionRollups(ctx)
	if err != nil {
		return err
	}
	if err := into.CopyDecisionRollups(ctx, rollups); err != nil {
		return err
	}
	if candidates > 0 || decisions > 0 {
		p.logger.Info("decisions moved to statistics.db", slog.Int("candidates", candidates), slog.Int("decisions", decisions))
	}
	return nil
}
