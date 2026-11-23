package app

import (
	"context"
	"errors"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/metadata/deezer"
	"github.com/makearr/Solarr/internal/metadata/fanart"
	"github.com/makearr/Solarr/internal/metadata/itunes"
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
	fanartClient := fanart.New(p.providerClient(providerFanart, nil, httpx.ClientConfig{}), p.providerKey(settings.MovieSourceFanart))
	fanartOn := func() bool {
		return p.cache.Snapshot().ProviderEnabled(settings.MovieSourceFanart) && fanartClient.Configured()
	}
	deezerClient := deezer.New(p.providerClient(providerDeezer, nil, httpx.ClientConfig{}))
	deezerOn := func() bool { return p.cache.Snapshot().ProviderEnabled(settings.SourceDeezer) }
	itunesClient := itunes.New(p.providerClient(providerITunes, nil, httpx.ClientConfig{}))
	itunesOn := func() bool { return p.cache.Snapshot().ProviderEnabled(settings.SourceITunesMusic) }
	audioDBOn := func() bool { return *p.cache.Snapshot().MetadataSettings().TheAudioDB }
	return music.NewArtworkChoice(music.ArtworkChoiceConfig{
		Artworks: library.NewArtworks(p.library),
		CoverArt: musicbrainz.NewCoverArt(client, coverArtBaseURL),
		AlbumImages: func(ctx context.Context, ask music.AlbumAsk) ([]media.ImageOption, error) {
			var options []media.ImageOption
			if audioDBOn() {
				more, err := audioDBOptions(audioDB.AlbumPictures(ctx, ask.ReleaseGroupMBID))
				if err != nil {
					return nil, err
				}
				options = append(options, more...)
			}
			if fanartOn() {
				options = append(options, fanartAlbumOptions(fanartClient.Album(ctx, ask.ReleaseGroupMBID))...)
			}
			if deezerOn() {
				if found, err := findDeezer(ctx, deezerClient, ask); err == nil && found.Cover != "" {
					options = append(options, media.ImageOption{URL: found.Cover, Source: "Deezer", Label: "Cover"})
				}
			}
			if itunesOn() && ask.Barcode != "" {
				if found, err := itunesClient.AlbumByUPC(ctx, ask.Barcode); err == nil {
					options = append(options, media.ImageOption{URL: found.Cover(), Source: "Apple Music", Label: "Cover"})
				}
			}
			return options, nil
		},
		ArtistImages: func(ctx context.Context, artistMBID string) ([]media.ImageOption, error) {
			var options []media.ImageOption
			if artist, err := provider.Artist(ctx, musicbrainz.PriorityInteractive, artistMBID); err == nil &&
				artist.WikidataQID != "" && *p.cache.Snapshot().MetadataSettings().Wikidata {
				if bio, err := biographies.Biography(ctx, artist.WikidataQID); err == nil && bio.ImageURL != "" {
					options = append(options, media.ImageOption{URL: bio.ImageURL, Source: "Wikidata", Label: "Photo"})
				}
			}
			if fanartOn() {
				options = append(options, fanartArtistOptions(fanartClient.Artist(ctx, artistMBID))...)
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
		HTTP: client, FS: p.fsys, ImagesDir: p.config.Paths.MediaCoverDir, Clock: clock.NewSystem(),
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

// fanartAlbumOptions is fanart.tv's covers and disc art of a release group; nothing known there is no options.
func fanartAlbumOptions(album fanart.Album, err error) []media.ImageOption {
	var options []media.ImageOption
	if err != nil {
		return nil
	}
	for _, group := range album.Albums {
		for _, image := range group.Covers {
			options = append(options, media.ImageOption{URL: image.URL, Source: "fanart.tv", Label: "Cover"})
		}
		for _, image := range group.Discs {
			options = append(options, media.ImageOption{URL: image.URL, Source: "fanart.tv", Label: "Disc"})
		}
	}
	return options
}

// fanartArtistOptions is fanart.tv's pictures of an artist, by kind; nothing known there is no options.
func fanartArtistOptions(artist fanart.Artist, err error) []media.ImageOption {
	if err != nil {
		return nil
	}
	var options []media.ImageOption
	for _, kind := range []struct {
		label  string
		images []fanart.Image
	}{{"Photo", artist.Thumbs}, {"Background", artist.Backgrounds}, {"Logo", artist.Logos}, {"Banner", artist.Banners}} {
		for _, image := range kind.images {
			options = append(options, media.ImageOption{URL: image.URL, Source: "fanart.tv", Label: kind.label})
		}
	}
	return options
}

// findDeezer looks an album up by its barcode when it has one, else by exact artist and title.
func findDeezer(ctx context.Context, client deezer.Client, ask music.AlbumAsk) (deezer.Album, error) {
	if ask.Barcode != "" {
		if found, err := client.ByUPC(ctx, ask.Barcode); err == nil {
			return found, nil
		}
	}
	if ask.Artist == "" || ask.Title == "" {
		return deezer.Album{}, deezer.ErrNotFound
	}
	return client.Find(ctx, ask.Artist, ask.Title)
}
