package adapters

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
)

// MusicNames finds the MusicBrainz ids of artists and albums Last.fm names by text; only an exact name counts, never a near one.
type MusicNames struct{ Provider musicbrainz.Provider }

// matches per search: the exact name is rarely further down than the first few.
const musicNameMatches = 5

// Artist implements lastfmfeeds.Names.
func (n MusicNames) Artist(ctx context.Context, name string) (string, bool, error) {
	matches, err := n.Provider.SearchArtists(ctx, musicbrainz.PriorityBackfill, name, musicNameMatches)
	if err != nil {
		return "", false, tolerated(err)
	}
	for _, match := range matches {
		if strings.EqualFold(match.Name, name) && match.MBID != "" {
			return match.MBID, true, nil
		}
	}
	return "", false, nil
}

// Album implements lastfmfeeds.Names.
func (n MusicNames) Album(ctx context.Context, artist, title string) (id string, year int, found bool, err error) {
	matches, err := n.Provider.Search(ctx, musicbrainz.PriorityBackfill, artist, title, musicNameMatches)
	if err != nil {
		return "", 0, false, tolerated(err)
	}
	for _, match := range matches {
		if strings.EqualFold(match.Title, title) && match.MBID != "" {
			if len(match.FirstReleaseDate) >= 4 {
				year, _ = strconv.Atoi(match.FirstReleaseDate[:4])
			}
			return match.MBID, year, true, nil
		}
	}
	return "", 0, false, nil
}

// tolerated turns "no source could search" into nothing found, which leaves the entry out; any other failure stops the page.
func tolerated(err error) error {
	if errors.Is(err, musicbrainz.ErrNotFound) {
		return nil
	}
	return err
}
