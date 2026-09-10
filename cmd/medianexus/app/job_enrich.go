package app

import (
	"context"
	"log/slog"

	"github.com/makearr/MediaNexus/internal/db/library"
	"github.com/makearr/MediaNexus/internal/jobs"
	"github.com/makearr/MediaNexus/internal/media/music"
	"github.com/makearr/MediaNexus/internal/platform/clock"
)

// registerEnrichJob wires the background metadata sweep into the registry and the
// executor: ADR-29's provider (the Servarr proxy first when enabled, direct
// MusicBrainz always -- it is what the proxy falls back to), permanent cache,
// music's interpretation of what comes back.
func (p *process) registerEnrichJob(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(music.EnrichDefinition); err != nil {
		return err
	}
	job := music.NewEnrichJob(music.EnrichJobConfig{
		Identity:  library.NewIdentity(p.library),
		People:    library.NewPeople(p.library),
		Provider:  p.musicBrainzProvider(),
		Biography: p.wikidataFetcher(),
		Clock:     clock.NewSystem(),
		Logger:    p.logger,
	})
	executor.Handle(music.EnrichJobType, job)
	return nil
}

// enqueueEnrichment submits one enrichment sweep. Registered as a task with
// internal/jobs.TaskRunner (todo 4.19), so it also fires on its own
// schedule, not only once at startup.
func (p *process) enqueueEnrichment(ctx context.Context) (jobs.EnqueueResult, error) {
	result, err := music.EnqueueEnrichment(ctx, p.queue)
	if err != nil {
		p.logger.Warn("enqueue music enrichment", slog.Any("error", err))
	}
	return result, err
}
