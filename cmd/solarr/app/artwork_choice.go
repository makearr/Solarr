package app

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
	"github.com/makearr/Solarr/internal/metadata/theaudiodb"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// artworkChoices is the image picker (M1g 1.3): what each provider holds for an album or artist.
func (p *process) artworkChoices() adapters.ArtworkChoices {
	client := p.providerClient(musicbrainz.CoverArtName, nil, httpx.ClientConfig{})
	audioDB := p.theAudioDB()
	biographies := p.wikidataFetcher()
	provider := p.musicBrainzProvider()
	audioDBOn := func() bool { return *p.cache.Snapshot().MetadataSettings().TheAudioDB }
	return adapters.ArtworkChoices{Choice: music.NewArtworkChoice(music.ArtworkChoiceConfig{
		Artworks: library.NewArtworks(p.library),
		CoverArt: musicbrainz.NewCoverArt(client, coverArtBaseURL),
		AlbumImages: func(ctx context.Context, releaseGroupMBID string) ([]music.ImageOption, error) {
			if !audioDBOn() {
				return nil, nil
			}
			return audioDBOptions(audioDB.AlbumPictures(ctx, releaseGroupMBID))
		},
		ArtistImages: func(ctx context.Context, artistMBID string) ([]music.ImageOption, error) {
			var options []music.ImageOption
			if artist, err := provider.Artist(ctx, musicbrainz.PriorityInteractive, artistMBID); err == nil &&
				artist.WikidataQID != "" && *p.cache.Snapshot().MetadataSettings().Wikidata {
				if bio, err := biographies.Biography(ctx, artist.WikidataQID); err == nil && bio.ImageURL != "" {
					options = append(options, music.ImageOption{URL: bio.ImageURL, Source: "Wikidata", Label: "Photo"})
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
	})}
}

// audioDBOptions is TheAudioDB's pictures as options; nothing known there is no options, not an error.
func audioDBOptions(pictures []theaudiodb.Picture, err error) ([]music.ImageOption, error) {
	if err != nil && !errors.Is(err, theaudiodb.ErrNotFound) {
		return nil, err
	}
	options := make([]music.ImageOption, len(pictures))
	for index, picture := range pictures {
		options[index] = music.ImageOption{URL: picture.URL, Source: "TheAudioDB", Label: picture.Label}
	}
	return options, nil
}
