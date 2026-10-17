package tv

import (
	"context"
	"log/slog"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
)

// CrossoverJobType is the registered type of the pass that finds episodes that are one broadcast listed under two series.
const CrossoverJobType = jobs.Type("tv.crossovers")

// CrossoverDefinition declares the pass: housekeeping over metadata already held, never in front of anything awaited.
var CrossoverDefinition = jobs.Definition{
	Name: "Finding crossover episodes",
	Type: CrossoverJobType, Pool: jobs.PoolHousekeeping, Mutating: true, MaxAttempts: 2,
}

// CrossoverJob links the episodes the evidence settles in each TV library (same title and day, lengths within 5%, a near
// identical cast), so a crossover is found without being asked; a person may still link or unlink by hand.
type CrossoverJob struct {
	Crossovers library.Crossovers
	// Libraries answers the ids of the TV libraries.
	Libraries func() []int64
	Logger    *slog.Logger
}

// Run implements jobs.Handler.
func (j CrossoverJob) Run(ctx context.Context, _ jobs.Job) error {
	linked := 0
	for _, categoryID := range j.Libraries() {
		made, err := j.Crossovers.LinkCertain(ctx, MediaType, categoryID)
		if err != nil {
			return err
		}
		linked += made
	}
	j.Logger.Info("crossover pass finished", slog.Int("linked", linked))
	return nil
}

// EnqueueCrossoverPass submits one pass; a second while one waits is the same work.
func EnqueueCrossoverPass(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{Type: CrossoverJobType, ActionKey: string(CrossoverJobType), Priority: jobs.PriorityLow})
}
