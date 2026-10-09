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
	// Person images come from the Commons hosts on a bucket of their own, apart from Wikidata's and Wikipedia's.
	imageClient := p.providerClient(providerWikimediaImage, nil, httpx.ClientConfig{})

	desired := library.NewDesired(p.library)
	organisations := library.NewOrganisations(p.library)
	job := music.NewEnrichJob(music.EnrichJobConfig{
		Identity:        library.NewIdentity(p.library),
		People:          library.NewPeople(p.library),
		Organisations:   &organisations,
		Provider:        p.musicBrainzProvider(),
		Biography:       p.wikidataFetcher(),
		WikidataEnabled: func() bool { return *p.cache.Snapshot().MetadataSettings().Wikidata },
		PeopleBackfillPerRun: func() int {
			return int(*p.cache.Snapshot().MetadataSettings().PeopleBackfillPerRun)
		},
		ArtistArt: p.theAudioDBArtist(p.theAudioDB()),
		Artworks:  library.NewArtworks(p.library),
		Realigner: realigner{p: p},
		Desired:   &desired,
		// Covers follow at once rather than at the six-hourly sweep (found 2026-09-28).
		OnRefined: func(ctx context.Context) {
			_, _ = p.enqueueCategoryCounts(ctx)
			_, _ = p.artworkPass(ctx, false)
			_, _ = p.enqueueTagWriteback(ctx)
		},
		Tags:      mediatool.New(nil, 0),
		Roots:     adapters.Roots{Cache: p.cache},
		HTTP:      imageClient,
		FS:        p.fsys,
		ImagesDir: filepath.Join(p.config.Paths.CacheDir, "images"),
		Clock:     clock.NewSystem(),
		Logger:    p.logger,
	})
	executor.Handle(music.EnrichJobType, job)
	if err := registry.Register(music.RefreshArtistDefinition); err != nil {
		return err
	}
	executor.Handle(music.RefreshArtistJobType, music.RefreshArtistJob{Enrich: job})
	if err := registry.Register(music.RefreshAlbumDefinition); err != nil {
		return err
	}
	executor.Handle(music.RefreshAlbumJobType, music.RefreshAlbumJob{Enrich: job})
	if err := registry.Register(music.RefreshArtistsDefinition); err != nil {
		return err
	}
	executor.Handle(music.RefreshArtistsJobType, music.RefreshArtistsJob{Enrich: job})
	return nil
}

// enrichmentPass starts music's enrichment sweep: its part of the "Refresh works" pass, and at once after a scan, an
// import or an add.
func (p *process) enrichmentPass(ctx context.Context, manual bool) (duplicate bool, err error) {
	result, err := music.EnqueueEnrichment(ctx, p.queue, manual)
	if err != nil {
		p.logger.Warn("enqueue music enrichment", slog.Any("error", err))
	}
	return result.Duplicate, err
}
