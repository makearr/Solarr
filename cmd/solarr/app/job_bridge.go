package app

import (
	"context"
	"log/slog"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/tv"
)

// registerBridgeJob wires the sweep that recognises the same human in TV and music.
func (p *process) registerBridgeJob(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(tv.BridgeDefinition); err != nil {
		return err
	}
	executor.Handle(tv.BridgeJobType, tv.NewBridgeJob(tv.BridgeConfig{People: library.NewPeople(p.library), Source: p.tmdbShared, Wikidata: p.wikidataFetcher(), Logger: p.logger}))
	return nil
}

// enqueueBridge submits one bridge sweep; registered as the "Match people across libraries" task.
func (p *process) enqueueBridge(ctx context.Context) (jobs.EnqueueResult, error) {
	result, err := tv.EnqueueBridge(ctx, p.queue)
	if err != nil {
		p.logger.Warn("enqueue people bridge", slog.Any("error", err))
	}
	return result, err
}

// enqueueTVRetry submits one retry of the files waiting for review; registered as the "Identify waiting TV files" task.
func (p *process) enqueueTVRetry(ctx context.Context) (jobs.EnqueueResult, error) {
	result, err := tv.EnqueueRetry(ctx, p.queue)
	if err != nil {
		p.logger.Warn("enqueue tv retry", slog.Any("error", err))
	}
	return result, err
}

// registerTVArtworkJob wires the sweep that keeps a TV library's posters, logos and photos in the picture cache.
func (p *process) registerTVArtworkJob(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(tv.ArtworkDefinition); err != nil {
		return err
	}
	executor.Handle(tv.ArtworkJobType, tv.NewArtworkJob(tv.ArtworkConfig{Reader: library.NewSeriesReader(p.library), Cache: p.remoteImages(), Logger: p.logger}))
	return nil
}

// enqueueTVArtwork submits one picture sweep; registered as the "Fetch series pictures" task.
func (p *process) enqueueTVArtwork(ctx context.Context) (jobs.EnqueueResult, error) {
	result, err := tv.EnqueueArtwork(ctx, p.queue)
	if err != nil {
		p.logger.Warn("enqueue tv artwork", slog.Any("error", err))
	}
	return result, err
}
