package app

import (
	"context"
	"log/slog"
	"path/filepath"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media"
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

// artworkJob is section 21.6's background cover art sweep.
func (p *process) artworkJob() media.JobRegistration {
	client := p.providerClient(musicbrainz.CoverArtName, nil, httpx.ClientConfig{})

	job := music.NewArtworkJob(music.ArtworkJobConfig{
		Artworks:        library.NewArtworks(p.library),
		CoverArt:        musicbrainz.NewCoverArt(client, coverArtBaseURL),
		Pictures:        mediatool.New(nil, 0),
		Roots:           adapters.Roots{Cache: p.cache},
		HTTP:            client,
		FS:              p.fsys,
		ImagesDir:       filepath.Join(p.config.Paths.CacheDir, "images"),
		Clock:           clock.NewSystem(),
		Logger:          p.logger,
		CoverSources:    func() []string { return p.cache.Snapshot().MetadataSettings().CoverSources },
		TheAudioDBCover: theAudioDBCover(p.theAudioDB()),
		OnDone:          func(ctx context.Context) { _, _ = p.enqueueTagWriteback(ctx) },
	})
	return media.JobRegistration{Definition: music.ArtworkDefinition, Handler: job}
}

// artworkPass starts music's cover art sweep: its part of the "Fetch pictures" pass, and after enrichment.
func (p *process) artworkPass(ctx context.Context, _ bool) (duplicate bool, err error) {
	result, err := music.EnqueueArtwork(ctx, p.queue)
	if err != nil {
		p.logger.Warn("enqueue music artwork", slog.Any("error", err))
	}
	return result.Duplicate, err
}
