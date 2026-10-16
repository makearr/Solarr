// Package radarrmeta reads Radarr's own movie metadata service (api.radarr.video), which needs no key: the keyless
// fallback behind TMDB for movies (stage 10). It is someone else's service, so it is asked politely and never relied on.
package radarrmeta

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

// ErrNotFound means the service knows no movie under that id.
var ErrNotFound = errors.New("radarrmeta: not found")

const (
	baseURL          = "https://api.radarr.video/v1"
	maxResponseBytes = 16 << 20
)

// Doer is the one method of httpx.Client this package needs.
type Doer interface {
	Do(ctx context.Context, request *http.Request) (*http.Response, error)
}

// Client asks the service.
type Client struct {
	http Doer
	base string
}

// New builds a client over an httpx client carrying the service's own rate bucket.
func New(client Doer) Client { return Client{http: client, base: baseURL} }

// Image is one picture of a kind: Poster, Fanart, Headshot, Logo.
type Image struct {
	CoverType string `json:"CoverType"`
	URL       string `json:"Url"`
}

// Credit is one cast or crew entry.
type Credit struct {
	Name       string  `json:"Name"`
	Order      int     `json:"Order"`
	Character  string  `json:"Character"`
	Job        string  `json:"Job"`
	Department string  `json:"Department"`
	TMDBID     int64   `json:"TmdbId"`
	Images     []Image `json:"Images"`
}

// Rating is one provider's score of the movie.
type Rating struct {
	Count int64   `json:"Count"`
	Value float64 `json:"Value"`
}

// Movie is one film as the service describes it; dates are RFC 3339 or empty.
type Movie struct {
	TMDBID            int64             `json:"TmdbId"`
	IMDbID            string            `json:"ImdbId"`
	Title             string            `json:"Title"`
	OriginalTitle     string            `json:"OriginalTitle"`
	OriginalLanguage  string            `json:"OriginalLanguage"`
	Overview          string            `json:"Overview"`
	Runtime           int               `json:"Runtime"`
	Year              int               `json:"Year"`
	Premiere          string            `json:"Premier"`
	InCinemas         string            `json:"InCinema"`
	PhysicalRelease   string            `json:"PhysicalRelease"`
	DigitalRelease    string            `json:"DigitalRelease"`
	Images            []Image           `json:"Images"`
	Genres            []string          `json:"Genres"`
	Keywords          []string          `json:"Keywords"`
	Studio            string            `json:"Studio"`
	Homepage          string            `json:"Homepage"`
	TrailerID         string            `json:"YoutubeTrailerId"`
	MovieRatings      map[string]Rating `json:"MovieRatings"`
	AlternativeTitles []struct {
		Title    string `json:"Title"`
		Language string `json:"Language"`
	} `json:"AlternativeTitles"`
	Certifications []struct {
		Country       string `json:"Country"`
		Certification string `json:"Certification"`
	} `json:"Certifications"`
	Credits struct {
		Cast []Credit `json:"Cast"`
		Crew []Credit `json:"Crew"`
	} `json:"Credits"`
	Collection *struct {
		TMDBID int64  `json:"TmdbId"`
		Name   string `json:"Name"`
	} `json:"Collection"`
}

// Movie reads one film by TMDB id.
func (c Client) Movie(ctx context.Context, tmdbID int64) (Movie, error) {
	var movie Movie
	return movie, c.get(ctx, "/movie/"+strconv.FormatInt(tmdbID, 10), nil, &movie)
}

// ByIMDb reads the film an IMDb id names.
func (c Client) ByIMDb(ctx context.Context, imdbID string) (Movie, error) {
	var movies []Movie
	if err := c.get(ctx, "/movie/imdb/"+url.PathEscape(imdbID), nil, &movies); err != nil {
		return Movie{}, err
	}
	if len(movies) == 0 {
		return Movie{}, ErrNotFound
	}
	return movies[0], nil
}

// Search finds films by name, narrowed to a release year when one is given.
func (c Client) Search(ctx context.Context, term string, year int) ([]Movie, error) {
	query := url.Values{"q": {term}}
	if year > 0 {
		query.Set("year", strconv.Itoa(year))
	}
	var movies []Movie
	return movies, c.get(ctx, "/search", query, &movies)
}

func (c Client) get(ctx context.Context, path string, query url.Values, into any) error {
	address := c.base + path
	if len(query) > 0 {
		address += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return fmt.Errorf("radarrmeta: %s: %w", path, err)
	}
	request.Header.Set("Accept", "application/json")
	response, err := c.http.Do(ctx, request)
	if err != nil {
		var refused *httpx.StatusError
		if errors.As(err, &refused) && refused.Status == http.StatusNotFound {
			return ErrNotFound
		}
		return fmt.Errorf("radarrmeta: %s: %w", path, err)
	}
	defer func() { _ = response.Body.Close() }()
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return ErrNotFound
	default:
		return fmt.Errorf("radarrmeta: %s: status %d", path, response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("radarrmeta: %s: read: %w", path, err)
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("radarrmeta: %s: decode: %w", path, err)
	}
	return nil
}
