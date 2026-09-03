package main

import (
	"context"
	"log/slog"
	"path/filepath"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/httpx"
	"github.com/makearr/MediaNexus/internal/jobs"
	"github.com/makearr/MediaNexus/internal/library"
	"github.com/makearr/MediaNexus/internal/music"
	"github.com/makearr/MediaNexus/internal/musicbrainz"
)

// coverArtBaseURL is fixed, not configurable: unlike MusicBrainz (ADR-29's proxy
// vs direct choice), Cover Art Archive has no alternative endpoint or auth to
// choose between, so there is nothing a setting would let an operator change.
const coverArtBaseURL = "https://coverartarchive.org"

// registerArtworkJob wires section 21.6's background cover art sweep into the
// registry and the executor.
func (p *process) registerArtworkJob(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(music.ArtworkDefinition); err != nil {
		return err
	}
	wall := clock.NewSystem()
	guard := httpx.NewGuard(nil, nil, nil)
	limiter := httpx.NewLimiter(wall, httpx.Config{}, nil)
	breaker := httpx.NewBreaker(wall, httpx.BreakerConfig{})
	client := httpx.NewClient(wall, guard, limiter, breaker, httpx.ClientConfig{
		Provider: musicbrainz.CoverArtName, UserAgent: httpx.UserAgent(version),
	})

	job := music.NewArtworkJob(music.ArtworkJobConfig{
		Artworks:  library.NewArtworks(p.library),
		CoverArt:  musicbrainz.NewCoverArt(client, coverArtBaseURL),
		HTTP:      client,
		FS:        p.fsys,
		ImagesDir: filepath.Join(p.config.Paths.CacheDir, "images"),
		Clock:     wall,
		Logger:    p.logger,
	})
	executor.Handle(music.ArtworkJobType, job)
	return nil
}

// enqueueArtwork submits one cover art sweep, once at startup -- the same
// no-scheduler-yet gap enqueueEnrichment carries.
func (p *process) enqueueArtwork(ctx context.Context) {
	if _, err := music.EnqueueArtwork(ctx, p.queue); err != nil {
		p.logger.Warn("enqueue music artwork", slog.Any("error", err))
	}
}
