package app

import (
	"context"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/statistics"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/platform/clock"
)

func (p *process) discographyCoverageJob() media.JobRegistration {
	return media.JobRegistration{Definition: music.DiscographyCoverageDefinition, Handler: music.NewDiscographyCoverageJob(music.DiscographyCoverageConfig{
		Statistics: library.NewStatistics(p.library), Coverage: statistics.NewRollup(p.statistics), Provider: p.musicBrainzProvider(),
		Categories: p.libraryIDsOf(string(music.MediaType)),
		Clock:      clock.NewSystem(), Logger: p.logger,
	})}
}

func (p *process) enqueueDiscographyCoverage(ctx context.Context) (jobs.EnqueueResult, error) {
	return p.queue.Enqueue(ctx, jobs.Request{
		Type: music.DiscographyCoverageJobType, ActionKey: string(music.DiscographyCoverageJobType),
		Priority: jobs.PriorityLow, Payload: []byte("{}"),
	})
}
