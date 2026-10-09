package app

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
	"github.com/makearr/Solarr/internal/metadata/theaudiodb"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// musicPictures is music's image picker (M1g 1.3): what each provider holds for an album or artist.
func (p *process) musicPictures() *music.ArtworkChoice {
	client := p.providerClient(musicbrainz.CoverArtName, nil, httpx.ClientConfig{})
	audioDB := p.theAudioDB()
	biographies := p.wikidataFetcher()
	provider := p.musicBrainzProvider()
	audioDBOn := func() bool { return *p.cache.Snapshot().MetadataSettings().TheAudioDB }
	return music.NewArtworkChoice(music.ArtworkChoiceConfig{
		Artworks: library.NewArtworks(p.library),
		CoverArt: musicbrainz.NewCoverArt(client, coverArtBaseURL),
		AlbumImages: func(ctx context.Context, releaseGroupMBID string) ([]media.ImageOption, error) {
			if !audioDBOn() {
				return nil, nil
			}
			return audioDBOptions(audioDB.AlbumPictures(ctx, releaseGroupMBID))
		},
		ArtistImages: func(ctx context.Context, artistMBID string) ([]media.ImageOption, error) {
			var options []media.ImageOption
			if artist, err := provider.Artist(ctx, musicbrainz.PriorityInteractive, artistMBID); err == nil &&
				artist.WikidataQID != "" && *p.cache.Snapshot().MetadataSettings().Wikidata {
				if bio, err := biographies.Biography(ctx, artist.WikidataQID); err == nil && bio.ImageURL != "" {
					options = append(options, media.ImageOption{URL: bio.ImageURL, Source: "Wikidata", Label: "Photo"})
				}
			}
			if audioDBOn() {
				more, err := audioDBOptions(audioDB.ArtistPictures(ctx, artistMBID))
				if err != nil && len(options) == 0 {
					return nil, err
				}
				options = append(options, more...)
			}
			return options, nil
		},
		HTTP: client, FS: p.fsys, ImagesDir: filepath.Join(p.config.Paths.CacheDir, "images"), Clock: clock.NewSystem(),
	})
}

// audioDBOptions is TheAudioDB's pictures as options; nothing known there is no options, not an error.
func audioDBOptions(pictures []theaudiodb.Picture, err error) ([]media.ImageOption, error) {
	if err != nil && !errors.Is(err, theaudiodb.ErrNotFound) {
		return nil, err
	}
	options := make([]media.ImageOption, len(pictures))
	for index, picture := range pictures {
		options[index] = media.ImageOption{URL: picture.URL, Source: "TheAudioDB", Label: picture.Label}
	}
	return options, nil
}
