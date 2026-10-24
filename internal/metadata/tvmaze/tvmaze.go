// Package tvmaze reads TVmaze (tvmaze.com), the keyless source of air times, episode lists, networks and cast
// (stage 8 T4, section 19.2). Bounded to its published limit of 20 calls per 10 seconds by the caller's httpx client.
package tvmaze

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

// ErrNotFound means TVmaze knows nothing for that id or lookup.
var ErrNotFound = errors.New("tvmaze: not found")

const (
	baseURL          = "https://api.tvmaze.com"
	maxResponseBytes = 16 << 20
)

// Doer is the one method of httpx.Client this package needs.
type Doer interface {
	Get(ctx context.Context, url string) (*http.Response, error)
}

// Client asks TVmaze.
type Client struct{ http Doer }

// New builds a client over an httpx client carrying TVmaze's own rate bucket.
func New(client Doer) Client { return Client{http: client} }

// Image is a picture at two sizes.
type Image struct {
	Medium   string `json:"medium"`
	Original string `json:"original"`
}

// Country is where a network is based; Timezone is an IANA name and what an air time is read in.
type Country struct {
	Name     string `json:"name"`
	Code     string `json:"code"`
	Timezone string `json:"timezone"`
}

// Network is a broadcaster (Network) or streaming channel (WebChannel); a web channel may have no country.
type Network struct {
	ID      int64    `json:"id"`
	Name    string   `json:"name"`
	Country *Country `json:"country"`
}

// Externals are the ids TVmaze holds for the same show elsewhere.
type Externals struct {
	TVRage  *int64 `json:"tvrage"`
	TheTVDB *int64 `json:"thetvdb"`
	IMDb    string `json:"imdb"`
}

// Schedule is when a show usually airs, in the network's timezone.
type Schedule struct {
	Time string   `json:"time"`
	Days []string `json:"days"`
}

// Rating is a score out of ten; Average is zero when unrated.
type Rating struct {
	Average float64 `json:"average"`
}

// Show is one series.
type Show struct {
	ID             int64     `json:"id"`
	Name           string    `json:"name"`
	Type           string    `json:"type"`
	Language       string    `json:"language"`
	Genres         []string  `json:"genres"`
	Status         string    `json:"status"`
	Runtime        int       `json:"runtime"`
	AverageRuntime int       `json:"averageRuntime"`
	Premiered      string    `json:"premiered"`
	Ended          string    `json:"ended"`
	Schedule       Schedule  `json:"schedule"`
	Rating         Rating    `json:"rating"`
	Network        *Network  `json:"network"`
	WebChannel     *Network  `json:"webChannel"`
	Externals      Externals `json:"externals"`
	Image          *Image    `json:"image"`
	Summary        string    `json:"summary"`
	Updated        int64     `json:"updated"`
}

// Episode is one episode or special; Number is nil for an unnumbered special.
type Episode struct {
	ID       int64   `json:"id"`
	Name     string  `json:"name"`
	Season   int     `json:"season"`
	Number   *int    `json:"number"`
	Type     string  `json:"type"` // regular, significant_special, insignificant_special
	Airdate  string  `json:"airdate"`
	Airtime  string  `json:"airtime"`
	Airstamp string  `json:"airstamp"` // RFC 3339, UTC; empty when unscheduled
	Runtime  int     `json:"runtime"`
	Image    *Image  `json:"image"`
	Summary  string  `json:"summary"`
	Rating   *Rating `json:"rating"`
}

// Season is one season's own record, with its poster.
type Season struct {
	ID           int64  `json:"id"`
	Number       int    `json:"number"`
	Name         string `json:"name"`
	EpisodeOrder *int   `json:"episodeOrder"`
	PremiereDate string `json:"premiereDate"`
	EndDate      string `json:"endDate"`
	Image        *Image `json:"image"`
}

// Person is a cast or crew member.
type Person struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Image    *Image `json:"image"`
	Birthday string `json:"birthday"`
}

// Character is who a cast member plays.
type Character struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Image *Image `json:"image"`
}

// CastMember pairs a person with their character.
type CastMember struct {
	Person    Person    `json:"person"`
	Character Character `json:"character"`
	Voice     bool      `json:"voice"`
}

// SearchResult is one show a text search found, with TVmaze's own relevance score.
type SearchResult struct {
	Score float64 `json:"score"`
	Show  Show    `json:"show"`
}

// Search finds shows by name.
func (c Client) Search(ctx context.Context, query string) ([]SearchResult, error) {
	var results []SearchResult
	return results, c.get(ctx, "/search/shows?q="+url.QueryEscape(query), &results)
}

// Show reads one series by TVmaze id.
func (c Client) Show(ctx context.Context, id int64) (Show, error) {
	var show Show
	return show, c.get(ctx, "/shows/"+strconv.FormatInt(id, 10), &show)
}

// LookupTVDB finds the show TVmaze holds for a TheTVDB id.
func (c Client) LookupTVDB(ctx context.Context, tvdbID int64) (Show, error) {
	var show Show
	return show, c.get(ctx, "/lookup/shows?thetvdb="+strconv.FormatInt(tvdbID, 10), &show)
}

// LookupIMDb finds the show TVmaze holds for an IMDb id such as "tt0000000".
func (c Client) LookupIMDb(ctx context.Context, imdbID string) (Show, error) {
	var show Show
	return show, c.get(ctx, "/lookup/shows?imdb="+url.QueryEscape(imdbID), &show)
}

// Episodes lists every episode, specials included, in aired order.
func (c Client) Episodes(ctx context.Context, id int64) ([]Episode, error) {
	var episodes []Episode
	return episodes, c.get(ctx, "/shows/"+strconv.FormatInt(id, 10)+"/episodes?specials=1", &episodes)
}

// Seasons lists the show's seasons.
func (c Client) Seasons(ctx context.Context, id int64) ([]Season, error) {
	var seasons []Season
	return seasons, c.get(ctx, "/shows/"+strconv.FormatInt(id, 10)+"/seasons", &seasons)
}

// Aliases lists the other names the show is known by.
func (c Client) Aliases(ctx context.Context, id int64) ([]string, error) {
	var parsed []struct {
		Name string `json:"name"`
	}
	if err := c.get(ctx, "/shows/"+strconv.FormatInt(id, 10)+"/akas", &parsed); err != nil {
		return nil, err
	}
	names := make([]string, len(parsed))
	for i, entry := range parsed {
		names[i] = entry.Name
	}
	return names, nil
}

// Cast lists the show's main cast.
func (c Client) Cast(ctx context.Context, id int64) ([]CastMember, error) {
	var cast []CastMember
	return cast, c.get(ctx, "/shows/"+strconv.FormatInt(id, 10)+"/cast", &cast)
}

func (c Client) get(ctx context.Context, path string, into any) error {
	response, err := c.http.Get(ctx, baseURL+path)
	if err != nil {
		var refused *httpx.StatusError
		if errors.As(err, &refused) && refused.Status == http.StatusNotFound {
			return ErrNotFound
		}
		return fmt.Errorf("tvmaze: %s: %w", path, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("tvmaze: %s: status %d", path, response.StatusCode)
	}
	body, err := httpx.ReadLimited(response.Body, maxResponseBytes)
	if err != nil {
		return fmt.Errorf("tvmaze: %s: read: %w", path, err)
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("tvmaze: %s: decode: %w", path, err)
	}
	return nil
}
