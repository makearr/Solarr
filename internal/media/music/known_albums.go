package music

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
)

// KnownAlbums implements knownfor.AlbumSource: Wikidata names the artist, MusicBrainz lists the albums.
type KnownAlbums struct {
	Wikidata interface {
		MusicBrainzArtists(ctx context.Context, qids []string) (map[string]string, error)
	}
	Provider musicbrainz.Provider
	Identity library.Identity
}

// ArtistFor answers the MusicBrainz artist id Wikidata records for the item.
func (k KnownAlbums) ArtistFor(ctx context.Context, qid string) (string, error) {
	if k.Wikidata == nil {
		return "", nil
	}
	qid = strings.ToUpper(qid)
	artists, err := k.Wikidata.MusicBrainzArtists(ctx, []string{qid})
	if err != nil {
		return "", err
	}
	return artists[qid], nil
}

// Albums lists the artist's studio albums and EPs the library does not hold, newest first, as Seerr's person page does.
func (k KnownAlbums) Albums(ctx context.Context, artistMBID string) ([]media.KnownFor, error) {
	groups, err := k.Provider.ArtistReleaseGroups(ctx, musicbrainz.PriorityInteractive, artistMBID)
	if err != nil {
		return nil, fmt.Errorf("musicbrainz: albums of %s: %w", artistMBID, err)
	}
	var albums []media.KnownFor
	for _, group := range groups {
		if (group.PrimaryType != "Album" && group.PrimaryType != "EP") || len(group.SecondaryTypes) > 0 {
			continue
		}
		if _, held, err := k.Identity.WorkByExternalID(ctx, ProviderReleaseGroup, group.MBID); err != nil || held {
			continue
		}
		albums = append(albums, media.KnownFor{
			Provider: ProviderReleaseGroup, ExternalID: group.MBID, Kind: "album", AddType: MediaType,
			Title: group.Title, Year: int64(yearOf(group.FirstReleaseDate)), Role: group.PrimaryType,
			PosterURL: "https://coverartarchive.org/release-group/" + group.MBID + "/front-250",
		})
	}
	slices.SortStableFunc(albums, func(a, b media.KnownFor) int { return int(b.Year - a.Year) })
	return albums, nil
}
