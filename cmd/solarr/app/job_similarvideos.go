package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
)

// similarVideosJobType is the registered type of the job that compares hashed videos for the duplicates screen.
const similarVideosJobType = jobs.Type("library.similar_videos")

const similarVideosActionKey = "library.similar_videos"

var similarVideosDefinition = jobs.Definition{
	Type: similarVideosJobType, Pool: jobs.PoolHousekeeping, Mutating: true, MaxAttempts: 2,
}

// similarVideosJob compares every hashed video at each level the screen offers and stores the groups with the time.
type similarVideosJob struct {
	process *process
}

func (j similarVideosJob) Run(ctx context.Context, _ jobs.Job) error {
	var roots []int64
	for _, folder := range j.process.cache.Snapshot().RootFolders() {
		roots = append(roots, folder.ID)
	}
	compared := j.process.similarVideos()
	for _, level := range library.SimilarityLevels {
		if err := compared.Refresh(ctx, roots, level, j.process.now().Unix()); err != nil {
			return fmt.Errorf("similar videos at %s: %w", level, err)
		}
	}
	j.process.logger.Info("similar videos compared", slog.Int("levels", len(library.SimilarityLevels)))
	return nil
}

func (p *process) registerSimilarVideosJob(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(similarVideosDefinition); err != nil {
		return err
	}
	executor.Handle(similarVideosJobType, similarVideosJob{process: p})
	return nil
}

// enqueueSimilarVideos is the scheduled task; queueSimilarVideos is what the screen's first look and Refresh button call.
func (p *process) enqueueSimilarVideos(ctx context.Context) (jobs.EnqueueResult, error) {
	return p.queue.Enqueue(ctx, jobs.Request{
		Type: similarVideosJobType, ActionKey: similarVideosActionKey, Priority: jobs.PriorityLow, Payload: []byte("{}"),
	})
}

func (p *process) queueSimilarVideos(ctx context.Context) error {
	_, err := p.enqueueSimilarVideos(ctx)
	return err
}
