package app

import (
	"context"
	"log/slog"
	"path/filepath"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
	"github.com/makearr/Solarr/internal/platform/mediatool"
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
		Provider: musicbrainz.CoverArtName, UserAgent: httpx.UserAgent(p.build.Version),
	})

	job := music.NewArtworkJob(music.ArtworkJobConfig{
		Artworks:     library.NewArtworks(p.library),
		CoverArt:     musicbrainz.NewCoverArt(client, coverArtBaseURL),
		Pictures:     mediatool.New(nil, 0),
		Roots:        adapters.Roots{Cache: p.cache},
		HTTP:         client,
		FS:           p.fsys,
		ImagesDir:    filepath.Join(p.config.Paths.CacheDir, "images"),
		Clock:        wall,
		Logger:       p.logger,
		CoverSources: func() []string { return p.cache.Snapshot().MetadataSettings().CoverSources },
	})
	executor.Handle(music.ArtworkJobType, job)
	return nil
}

// enqueueArtwork submits one cover art sweep. Registered as a task with
// internal/jobs.TaskRunner (todo 4.19), so it also fires on its own
// schedule, not only once at startup.
func (p *process) enqueueArtwork(ctx context.Context) (jobs.EnqueueResult, error) {
	result, err := music.EnqueueArtwork(ctx, p.queue)
	if err != nil {
		p.logger.Warn("enqueue music artwork", slog.Any("error", err))
	}
	return result, err
}
