package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/httpx"
	"github.com/makearr/MediaNexus/internal/jobs"
	"github.com/makearr/MediaNexus/internal/library"
	"github.com/makearr/MediaNexus/internal/metacache"
	"github.com/makearr/MediaNexus/internal/music"
	"github.com/makearr/MediaNexus/internal/musicbrainz"
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
		Identity: library.NewIdentity(p.library),
		Provider: p.musicBrainzProvider(),
		Clock:    clock.NewSystem(),
		Logger:   p.logger,
	})
	executor.Handle(music.EnrichJobType, job)
	return nil
}

// musicBrainzProvider builds ADR-29's provider: one shared rate-limit and circuit
// breaker registry (ADR-13) across both sources, so a proxy failure counts against
// only the proxy's own budget and never steals from direct MusicBrainz's.
func (p *process) musicBrainzProvider() musicbrainz.Provider {
	wall := clock.NewSystem()
	guard := httpx.NewGuard(nil, nil, nil)
	limiter := httpx.NewLimiter(wall, httpx.Config{
		Providers: map[string]httpx.Limits{
			// MusicBrainz allows one request per second and means it (ADR-29).
			musicbrainz.DirectName: {MinInterval: time.Second},
			// The proxy is Lidarr's own service, not rate-limited by ADR-29's
			// 1 req/s rule; kept modest anyway since it is shared infrastructure
			// this project does not operate.
			musicbrainz.ProxyName: {MinInterval: 250 * time.Millisecond},
		},
	}, nil)
	breaker := httpx.NewBreaker(wall, httpx.BreakerConfig{})

	agent := httpx.UserAgent(version)
	config := p.config.Metadata.MusicBrainz
	direct := musicbrainz.NewDirect(httpx.NewClient(wall, guard, limiter, breaker, httpx.ClientConfig{
		Provider: musicbrainz.DirectName, UserAgent: agent,
	}), config.BaseURL)

	sources := []musicbrainz.Source{direct}
	if config.ProxyEnabled {
		proxy := musicbrainz.NewProxy(httpx.NewClient(wall, guard, limiter, breaker, httpx.ClientConfig{
			Provider: musicbrainz.ProxyName, UserAgent: agent,
		}), config.ProxyBaseURL)
		// The proxy goes first (ADR-29: "preferred, never required") -- direct
		// MusicBrainz is what a proxy failure (the breaker opening) falls back
		// to, so it stays in the list either way.
		sources = []musicbrainz.Source{proxy, direct}
	}

	return musicbrainz.New(musicbrainz.Config{
		Sources: sources, Cache: metacache.New(p.metadata), Clock: wall, Logger: p.logger,
		RefreshAfter: config.RefreshAfter.AsDuration(),
	})
}

// enqueueEnrichment submits one enrichment sweep, once at startup -- there is no
// scheduler yet (the same gap enqueueLibraryScans carries), so a placeholder
// edition a scan creates while this process is already running waits for the next
// restart to be picked up.
func (p *process) enqueueEnrichment(ctx context.Context) {
	if _, err := music.EnqueueEnrichment(ctx, p.queue); err != nil {
		p.logger.Warn("enqueue music enrichment", slog.Any("error", err))
	}
}
