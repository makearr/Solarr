package app

import (
	"context"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/statistics"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/platform/clock"
)

func (p *process) registerDiscographyCoverageJob(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(music.DiscographyCoverageDefinition); err != nil {
		return err
	}
	executor.Handle(music.DiscographyCoverageJobType, music.NewDiscographyCoverageJob(music.DiscographyCoverageConfig{
		Statistics: library.NewStatistics(p.library), Coverage: statistics.NewRollup(p.statistics), Provider: p.musicBrainzProvider(),
		Categories: func() []int64 {
			var ids []int64
			for _, category := range p.cache.Snapshot().Categories() {
				if category.MediaType == "music" {
					ids = append(ids, category.ID)
				}
			}
			return ids
		},
		Clock: clock.NewSystem(), Logger: p.logger,
	}))
	return nil
}

func (p *process) enqueueDiscographyCoverage(ctx context.Context) (jobs.EnqueueResult, error) {
	return p.queue.Enqueue(ctx, jobs.Request{
		Type: music.DiscographyCoverageJobType, ActionKey: string(music.DiscographyCoverageJobType),
		Priority: jobs.PriorityLow, Payload: []byte("{}"),
	})
}
