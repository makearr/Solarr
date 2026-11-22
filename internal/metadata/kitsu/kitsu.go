// Package kitsu reads Kitsu (kitsu.io), an anime database that needs no key: synopsis, age rating, average rating, poster
// and the ids its mappings name on AniList, MyAnimeList, AniDB and TheTVDB.
package kitsu

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

// ErrNotFound means Kitsu knows no anime under that id.
var ErrNotFound = errors.New("kitsu: not found")

const (
	baseURL          = "https://kitsu.io/api/edge"
	maxResponseBytes = 2 << 20
)

// Getter is the one method of httpx.Client this package needs.
type Getter interface {
	GetJSON(ctx context.Context, address string, limit int64, into any) error
}

// Client asks Kitsu, which needs no key.
type Client struct {
	http Getter
	base string
}

// New builds a client.
func New(client Getter) Client { return Client{http: client, base: baseURL} }

// Anime is one entry as Kitsu describes it; AverageRating is 0..100 as Kitsu writes it, "" where none.
type Anime struct {
	ID            string
	Slug          string
	Title         string
	Synopsis      string
	AgeRating     string
	AverageRating string
	UserCount     int
	StartDate     string
	EpisodeCount  int
	EpisodeLength int
	Poster        string
	// Mappings are the other sites' ids by site name ("myanimelist/anime", "anidb", "thetvdb/series", "anilist/anime").
	Mappings map[string]string
}

type wire struct {
	Data     []wireResource `json:"data"`
	Included []wireResource `json:"included"`
}

type wireOne struct {
	Data     wireResource   `json:"data"`
	Included []wireResource `json:"included"`
}

type wireResource struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Attributes struct {
		Slug          string `json:"slug"`
		Canonical     string `json:"canonicalTitle"`
		Synopsis      string `json:"synopsis"`
		AgeRating     string `json:"ageRating"`
		AverageRating string `json:"averageRating"`
		UserCount     int    `json:"userCount"`
		StartDate     string `json:"startDate"`
		EpisodeCount  int    `json:"episodeCount"`
		EpisodeLength int    `json:"episodeLength"`
		Poster        *struct {
			Original string `json:"original"`
			Large    string `json:"large"`
		} `json:"posterImage"`
		ExternalSite string `json:"externalSite"`
		ExternalID   string `json:"externalId"`
	} `json:"attributes"`
}

func (r wireResource) anime(mappings []wireResource) Anime {
	a := r.Attributes
	out := Anime{ID: r.ID, Slug: a.Slug, Title: a.Canonical, Synopsis: a.Synopsis, AgeRating: a.AgeRating, AverageRating: a.AverageRating,
		UserCount: a.UserCount, StartDate: a.StartDate, EpisodeCount: a.EpisodeCount, EpisodeLength: a.EpisodeLength, Mappings: map[string]string{}}
	if a.Poster != nil {
		out.Poster = a.Poster.Original
		if out.Poster == "" {
			out.Poster = a.Poster.Large
		}
	}
	for _, mapping := range mappings {
		if mapping.Type == "mappings" && mapping.Attributes.ExternalSite != "" {
			if _, seen := out.Mappings[mapping.Attributes.ExternalSite]; !seen {
				out.Mappings[mapping.Attributes.ExternalSite] = mapping.Attributes.ExternalID
			}
		}
	}
	return out
}

func (c Client) get(ctx context.Context, path string, query url.Values, into any) error {
	address := c.base + path
	if len(query) > 0 {
		address += "?" + query.Encode()
	}
	err := c.http.GetJSON(ctx, address, maxResponseBytes, into)
	switch {
	case errors.Is(err, httpx.ErrNotFound):
		return ErrNotFound
	case err != nil:
		return fmt.Errorf("kitsu: %w", err)
	}
	return nil
}

// ByAniList finds the entry Kitsu maps to an AniList anime id, with every mapping it holds.
func (c Client) ByAniList(ctx context.Context, anilistID int64) (Anime, error) {
	var found wire
	query := url.Values{"filter[externalSite]": {"anilist/anime"}, "filter[externalId]": {strconv.FormatInt(anilistID, 10)}, "include": {"item"}}
	if err := c.get(ctx, "/mappings", query, &found); err != nil {
		return Anime{}, err
	}
	for _, item := range found.Included {
		if item.Type == "anime" {
			return c.withMappings(ctx, item)
		}
	}
	return Anime{}, ErrNotFound
}

// Search finds the entry whose title is the nearest to the one given: Kitsu's own ranking, the first hit.
func (c Client) Search(ctx context.Context, title string) (Anime, error) {
	if strings.TrimSpace(title) == "" {
		return Anime{}, ErrNotFound
	}
	var found wire
	if err := c.get(ctx, "/anime", url.Values{"filter[text]": {title}, "page[limit]": {"1"}}, &found); err != nil {
		return Anime{}, err
	}
	if len(found.Data) == 0 {
		return Anime{}, ErrNotFound
	}
	return c.withMappings(ctx, found.Data[0])
}

func (c Client) withMappings(ctx context.Context, item wireResource) (Anime, error) {
	var found wireOne
	query := url.Values{"include": {"mappings"}, "fields[mappings]": {"externalSite,externalId"}, "fields[anime]": {"canonicalTitle"}}
	if err := c.get(ctx, "/anime/"+url.PathEscape(item.ID), query, &found); err != nil {
		return item.anime(nil), nil //nolint:nilerr // the entry itself was read; only its mappings are missing
	}
	return item.anime(found.Included), nil
}
