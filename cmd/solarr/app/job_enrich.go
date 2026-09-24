package app

import (
	"context"
	"log/slog"
	"path/filepath"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// registerEnrichJob wires the background metadata sweep into the registry and the
// executor: ADR-29's provider (the Servarr proxy first when enabled, direct
// MusicBrainz always -- it is what the proxy falls back to), permanent cache,
// music's interpretation of what comes back.
func (p *process) registerEnrichJob(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(music.EnrichDefinition); err != nil {
		return err
	}
	wall := clock.NewSystem()
	// Person images ride the same Commons/Wikimedia hosts biographies
	// already fetch from -- its own guard/limiter/breaker, not shared with
	// wikidataFetcher's two clients, the same one-bucket-per-host reasoning
	// every other provider in this codebase already follows (ADR-13).
	guard := httpx.NewGuard(nil, nil, nil)
	limiter := httpx.NewLimiter(wall, httpx.Config{}, nil)
	breaker := httpx.NewBreaker(wall, httpx.BreakerConfig{})
	imageClient := httpx.NewClient(wall, guard, limiter, breaker, httpx.ClientConfig{
		Provider: "wikimedia-image", UserAgent: httpx.UserAgent(p.build.Version), Proxy: p.liveProxyPolicy(),
	})

	job := music.NewEnrichJob(music.EnrichJobConfig{
		Identity:        library.NewIdentity(p.library),
		People:          library.NewPeople(p.library),
		Provider:        p.musicBrainzProvider(),
		Biography:       p.wikidataFetcher(),
		WikidataEnabled: func() bool { return *p.cache.Snapshot().MetadataSettings().Wikidata },
		Artworks:        library.NewArtworks(p.library),
		Realigner:       realigner{p: p},
		Tags:            mediatool.New(nil, 0),
		Roots:           adapters.Roots{Cache: p.cache},
		HTTP:            imageClient,
		FS:              p.fsys,
		ImagesDir:       filepath.Join(p.config.Paths.CacheDir, "images"),
		Clock:           wall,
		Logger:          p.logger,
	})
	executor.Handle(music.EnrichJobType, job)
	return nil
}

// enqueueEnrichment submits one enrichment sweep. Registered as a task with
// internal/jobs.TaskRunner (todo 4.19), so it also fires on its own
// schedule, not only once at startup.
func (p *process) enqueueEnrichment(ctx context.Context) (jobs.EnqueueResult, error) {
	return p.enqueueEnrichmentRun(ctx, false)
}

// enqueueManualEnrichment is the scheduler's "Run now": a manual refresh.
func (p *process) enqueueManualEnrichment(ctx context.Context) (jobs.EnqueueResult, error) {
	return p.enqueueEnrichmentRun(ctx, true)
}

func (p *process) enqueueEnrichmentRun(ctx context.Context, manual bool) (jobs.EnqueueResult, error) {
	result, err := music.EnqueueEnrichment(ctx, p.queue, manual)
	if err != nil {
		p.logger.Warn("enqueue music enrichment", slog.Any("error", err))
	}
	return result, err
}
