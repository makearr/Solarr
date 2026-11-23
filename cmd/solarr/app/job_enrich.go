package app

import (
	"context"
	"errors"
	"log/slog"
	"slices"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/metadata/deezer"
	"github.com/makearr/Solarr/internal/metadata/discogs"
	"github.com/makearr/Solarr/internal/metadata/lastfm"
	"github.com/makearr/Solarr/internal/metadata/listenbrainz"
	"github.com/makearr/Solarr/internal/metadata/spotify"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// enrichJobs are the background metadata sweep and its refresh variants: ADR-29's provider (the Servarr proxy first when
// enabled, direct MusicBrainz always -- it is what the proxy falls back to), permanent cache, music's interpretation of
// what comes back.
func (p *process) enrichJobs() []media.JobRegistration {
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
		ArtistArt:    music.DefinitionArtistArt(p.theAudioDBArtist(p.theAudioDB()), p.enrichmentRecords),
		AlbumRatings: p.albumRatings(),
		Artworks:     library.NewArtworks(p.library),
		Realigner:    realigner{p: p},
		Libraries: adapters.EntityLibraries{
			MediaType: string(music.MediaType), Works: library.NewWorks(p.library), People: library.NewPeople(p.library), Cache: p.cache,
		},
		Desired: &desired,
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
		ImagesDir: p.config.Paths.MediaCoverDir,
		Clock:     clock.NewSystem(),
		Logger:    p.logger,
	})
	return []media.JobRegistration{
		{Definition: music.EnrichDefinition, Handler: job},
		{Definition: music.RefreshArtistDefinition, Handler: music.RefreshArtistJob{Enrich: job}},
		{Definition: music.RefreshAlbumDefinition, Handler: music.RefreshAlbumJob{Enrich: job}},
		{Definition: music.RefreshArtistsDefinition, Handler: music.RefreshArtistsJob{Enrich: job}},
	}
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

// albumRatings is the popularity ListenBrainz and Deezer hold of an album, each asked only while switched on; both are keyless.
func (p *process) albumRatings() music.AlbumRatings {
	lb := listenbrainz.New(p.providerClient(providerListenBrainz, nil, httpx.ClientConfig{}))
	dz := deezer.New(p.providerClient(providerDeezer, nil, httpx.ClientConfig{}))
	lf := lastfm.New(p.providerClient(providerLastFM, nil, httpx.ClientConfig{}), p.providerKey(settings.SourceLastFM))
	dc := discogs.New(p.providerClient(providerDiscogs, nil, httpx.ClientConfig{}), p.providerKey(settings.SourceDiscogs))
	sp := spotify.New(p.providerClient(providerSpotify, nil, httpx.ClientConfig{}), p.providerKey(settings.SourceSpotify), clock.NewSystem().Now)
	return func(ctx context.Context, releaseGroupMBID, artist, title string) []music.AlbumRating {
		snapshot := p.cache.Snapshot()
		var ratings []music.AlbumRating
		if snapshot.ProviderEnabled(settings.SourceListenBrainz) && releaseGroupMBID != "" {
			found, err := lb.ReleaseGroup(ctx, releaseGroupMBID)
			switch {
			case err == nil:
				ratings = append(ratings, music.AlbumRating{Provider: settings.SourceListenBrainz, Score: music.PopularityScore(found.Listeners), Votes: found.Listeners})
			case !errors.Is(err, listenbrainz.ErrNotFound):
				p.logger.Warn("listenbrainz popularity", slog.String("mbid", releaseGroupMBID), slog.Any("error", err))
			}
		}
		if snapshot.ProviderEnabled(settings.SourceDeezer) && artist != "" && title != "" {
			found, err := dz.Find(ctx, artist, title)
			switch {
			case err == nil && found.Fans > 0:
				ratings = append(ratings, music.AlbumRating{Provider: settings.SourceDeezer, Score: music.PopularityScore(found.Fans), Votes: found.Fans})
			case err != nil && !errors.Is(err, deezer.ErrNotFound):
				p.logger.Warn("deezer album", slog.String("mbid", releaseGroupMBID), slog.Any("error", err))
			}
		}
		if snapshot.ProviderEnabled(settings.SourceLastFM) && lf.Configured() && artist != "" && title != "" {
			found, err := lf.AlbumInfo(ctx, artist, title)
			switch {
			case err == nil && found.Listeners > 0:
				ratings = append(ratings, music.AlbumRating{Provider: settings.SourceLastFM, Score: music.PopularityScore(found.Listeners), Votes: found.Listeners})
			case err != nil && !errors.Is(err, lastfm.ErrNotFound):
				p.logger.Warn("lastfm album", slog.String("mbid", releaseGroupMBID), slog.Any("error", err))
			}
		}
		if snapshot.ProviderEnabled(settings.SourceDiscogs) && dc.Configured() && artist != "" && title != "" {
			found, err := dc.Find(ctx, artist, title)
			switch {
			case err == nil && found.Votes > 0:
				// Discogs rates from 0 to 5; the library scale is 0 to 10.
				ratings = append(ratings, music.AlbumRating{Provider: settings.SourceDiscogs, Score: found.Rating * 2, Votes: found.Votes})
			case err != nil && !errors.Is(err, discogs.ErrNotFound):
				p.logger.Warn("discogs release", slog.String("mbid", releaseGroupMBID), slog.Any("error", err))
			}
		}
		if snapshot.ProviderEnabled(settings.SourceSpotify) && sp.Configured() && artist != "" && title != "" {
			found, err := sp.FindAlbum(ctx, artist, title)
			switch {
			case err == nil && found.Popularity > 0:
				// Spotify's popularity runs 0 to 100; it counts no votes.
				ratings = append(ratings, music.AlbumRating{Provider: settings.SourceSpotify, Score: float64(found.Popularity) / 10})
			case err != nil && !errors.Is(err, spotify.ErrNotFound):
				p.logger.Warn("spotify album", slog.String("mbid", releaseGroupMBID), slog.Any("error", err))
			}
		}
		return ratings
	}
}

// enrichmentRecords are the switched-on definitions that declare music and the enrich role, in key order.
func (p *process) enrichmentRecords() []music.RecordProvider {
	held := p.records.held.Load()
	if held == nil {
		return nil
	}
	snapshot := p.cache.Snapshot()
	var keys []string
	for key, entry := range *held {
		if entry.parsed.Role == string(settings.RoleEnrich) && slices.Contains(entry.parsed.MediaTypes, string(music.MediaType)) && snapshot.ProviderEnabled(key) {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	providers := make([]music.RecordProvider, 0, len(keys))
	for _, key := range keys {
		providers = append(providers, (*held)[key].provider)
	}
	return providers
}
