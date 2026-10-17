package tvdb

import (
	"context"
	"net/url"
	"strconv"
	"strings"
)

// Movie is one film as TheTVDB's extended answer carries it, with its translations.
type Movie struct {
	ID               int64      `json:"id"`
	Name             string     `json:"name"`
	Year             string     `json:"year"`
	Runtime          int        `json:"runtime"`
	OriginalCountry  string     `json:"originalCountry"`
	OriginalLanguage string     `json:"originalLanguage"`
	Genres           []Named    `json:"genres"`
	RemoteIDs        []RemoteID `json:"remoteIds"`
	Artworks         []Artwork  `json:"artworks"`
	Translations     struct {
		Names     []Translation `json:"nameTranslations"`
		Overviews []Translation `json:"overviewTranslations"`
	} `json:"translations"`
}

// Translation is a film's name or overview in one language (ISO 639-2, "eng").
type Translation struct {
	Language string `json:"language"`
	Name     string `json:"name"`
	Overview string `json:"overview"`
}

// Overview answers the film's overview in a language, empty when TheTVDB has none.
func (m Movie) Overview(language string) string {
	for _, translation := range m.Translations.Overviews {
		if translation.Language == language {
			return translation.Overview
		}
	}
	return ""
}

// RemoteID answers the film's id at a named site ("IMDB", "TheMovieDB.com"), empty when none.
func (m Movie) RemoteID(sourceName string) string {
	for _, remote := range m.RemoteIDs {
		if strings.EqualFold(remote.SourceName, sourceName) {
			return remote.ID
		}
	}
	return ""
}

// Movie reads one film with its artwork and translations.
func (c Client) Movie(ctx context.Context, id int64) (Movie, error) {
	var movie Movie
	return movie, c.get(ctx, "/movies/"+strconv.FormatInt(id, 10)+"/extended", url.Values{"meta": {"translations"}, "short": {"false"}}, &movie)
}

// MovieByRemoteID finds the film another site's id names (an IMDb id, a TMDB id); false when TheTVDB has none.
func (c Client) MovieByRemoteID(ctx context.Context, remoteID string) (int64, bool, error) {
	var hits []struct {
		Movie *struct {
			ID int64 `json:"id"`
		} `json:"movie"`
	}
	if err := c.get(ctx, "/search/remoteid/"+url.PathEscape(remoteID), nil, &hits); err != nil {
		return 0, false, err
	}
	for _, hit := range hits {
		if hit.Movie != nil && hit.Movie.ID > 0 {
			return hit.Movie.ID, true, nil
		}
	}
	return 0, false, nil
}
