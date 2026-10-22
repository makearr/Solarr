package app

import (
	"context"
	"log/slog"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/tv"
)

// bridgeJob is the sweep that recognises the same human in TV and music; tvService first, which builds the TMDB source.
func (p *process) bridgeJob() media.JobRegistration {
	p.tvService()
	return media.JobRegistration{Definition: tv.BridgeDefinition, Handler: tv.NewBridgeJob(tv.BridgeConfig{People: library.NewPeople(p.library), Source: p.tmdbShared, Wikidata: p.wikidataFetcher(), Logger: p.logger})}
}

// enqueueBridge submits one bridge sweep; registered as the "Match people across libraries" task.
func (p *process) enqueueBridge(ctx context.Context) (jobs.EnqueueResult, error) {
	result, err := tv.EnqueueBridge(ctx, p.queue)
	if err != nil {
		p.logger.Warn("enqueue people bridge", slog.Any("error", err))
	}
	return result, err
}

// tvRetryTask is the "Identify waiting TV files" task, under the key TV's descriptor names for the Retry button.
func (p *process) tvRetryTask() jobs.Task {
	return jobs.Task{
		Key: tv.RetryScheduleKey, Name: "Identify waiting TV files", DefaultCadence: tvRetryScheduleCron,
		Tier: string(settings.TierNormal), Category: jobs.CategoryLibrary, Enqueue: p.enqueueTVRetry,
	}
}

// enqueueTVRetry submits one retry of the files waiting for review; registered as the "Identify waiting TV files" task.
func (p *process) enqueueTVRetry(ctx context.Context) (jobs.EnqueueResult, error) {
	result, err := tv.EnqueueRetry(ctx, p.queue)
	if err != nil {
		p.logger.Warn("enqueue tv retry", slog.Any("error", err))
	}
	return result, err
}

// tvArtworkJob is the sweep that keeps a TV library's posters, logos and photos in the picture cache.
func (p *process) tvArtworkJob() media.JobRegistration {
	return media.JobRegistration{Definition: tv.ArtworkDefinition, Handler: tv.NewArtworkJob(library.NewSeriesReader(p.library), p.remoteImages(), p.logger)}
}
