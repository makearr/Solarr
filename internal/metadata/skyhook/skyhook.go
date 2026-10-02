// Package skyhook reads Sonarr's TheTVDB proxy (skyhook.sonarr.tv): series, episodes, cast and artwork with no
// key. Preferred and never required, like the Servarr proxy for music (stage 8 T4).
package skyhook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

// ErrNotFound means the proxy knows no series under that id.
var ErrNotFound = errors.New("skyhook: not found")

const (
	baseURL          = "https://skyhook.sonarr.tv/v1/tvdb"
	maxResponseBytes = 32 << 20
)

// Doer is the one method of httpx.Client this package needs.
type Doer interface {
	Get(ctx context.Context, url string) (*http.Response, error)
}

// Client asks the proxy; Language is the three-letter code titles and overviews are returned in ("en" by default).
type Client struct {
	http     Doer
	language string
}

// New builds a client over an httpx client carrying the proxy's own rate bucket.
func New(client Doer, language string) Client {
	if language == "" {
		language = "en"
	}
	return Client{http: client, language: language}
}

// Image is one picture of a kind: Poster, Banner, FanArt, Logo, Clearart, Icon.
type Image struct {
	CoverType string `json:"coverType"`
	URL       string `json:"url"`
}

// Actor is a cast member and the character played.
type Actor struct {
	Name      string `json:"name"`
	Character string `json:"character"`
	Image     string `json:"image"`
}

// Season carries the season's artwork; the proxy lists a season for every number an episode uses, specials as 0.
type Season struct {
	SeasonNumber int     `json:"seasonNumber"`
	Images       []Image `json:"images"`
}

// Episode is one episode in TheTVDB's aired order. AiredBefore* place a special between two regular episodes.
type Episode struct {
	TVDBShowID               int64  `json:"tvdbShowId"`
	TVDBID                   int64  `json:"tvdbId"`
	SeasonNumber             int    `json:"seasonNumber"`
	EpisodeNumber            int    `json:"episodeNumber"`
	AbsoluteEpisodeNumber    *int   `json:"absoluteEpisodeNumber"`
	AiredBeforeSeasonNumber  *int   `json:"airedBeforeSeasonNumber"`
	AiredBeforeEpisodeNumber *int   `json:"airedBeforeEpisodeNumber"`
	Title                    string `json:"title"`
	AirDate                  string `json:"airDate"`
	AirDateUTC               string `json:"airDateUtc"`
	Runtime                  int    `json:"runtime"`
	FinaleType               string `json:"finaleType"`
	Overview                 string `json:"overview"`
	Image                    string `json:"image"`
}

// TimeOfDay is when the show usually airs, in the network's local time.
type TimeOfDay struct {
	Hours   int `json:"hours"`
	Minutes int `json:"minutes"`
}

// Rating is the score and how many votes it rests on; the proxy sends the value as text.
type Rating struct {
	Count int    `json:"count"`
	Value string `json:"value"`
}

// Series is one show with its episodes, cast and artwork.
type Series struct {
	TVDBID           int64      `json:"tvdbId"`
	Title            string     `json:"title"`
	Overview         string     `json:"overview"`
	Slug             string     `json:"slug"`
	OriginalCountry  string     `json:"originalCountry"`
	OriginalLanguage string     `json:"originalLanguage"`
	Language         string     `json:"language"`
	FirstAired       string     `json:"firstAired"`
	LastAired        string     `json:"lastAired"`
	TVRageID         int64      `json:"tvRageId"`
	TVMazeID         int64      `json:"tvMazeId"`
	TMDBID           int64      `json:"tmdbId"`
	IMDbID           string     `json:"imdbId"`
	MALIDs           []int64    `json:"malIds"`
	AniDBIDs         []int64    `json:"anidbIds"`
	AniListIDs       []int64    `json:"aniListIds"`
	LastUpdated      time.Time  `json:"lastUpdated"`
	Status           string     `json:"status"`
	Runtime          int        `json:"runtime"`
	TimeOfDay        *TimeOfDay `json:"timeOfDay"`
	OriginalNetwork  string     `json:"originalNetwork"`
	Network          string     `json:"network"`
	Genres           []string   `json:"genres"`
	ContentRating    string     `json:"contentRating"`
	Rating           Rating     `json:"rating"`
	Actors           []Actor    `json:"actors"`
	Images           []Image    `json:"images"`
	Seasons          []Season   `json:"seasons"`
	Episodes         []Episode  `json:"episodes"`
}

// Series reads one show by TheTVDB id, with every episode.
func (c Client) Series(ctx context.Context, tvdbID int64) (Series, error) {
	var series Series
	return series, c.get(ctx, "/shows/"+url.PathEscape(c.language)+"/"+strconv.FormatInt(tvdbID, 10), &series)
}

// Search finds shows by name; hits carry the show's own fields but no episodes.
func (c Client) Search(ctx context.Context, term string) ([]Series, error) {
	var hits []Series
	return hits, c.get(ctx, "/search/"+url.PathEscape(c.language)+"/?term="+url.QueryEscape(term), &hits)
}

func (c Client) get(ctx context.Context, path string, into any) error {
	response, err := c.http.Get(ctx, baseURL+path)
	if err != nil {
		var refused *httpx.StatusError
		if errors.As(err, &refused) && refused.Status == http.StatusNotFound {
			return ErrNotFound
		}
		return fmt.Errorf("skyhook: %s: %w", path, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("skyhook: %s: status %d", path, response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("skyhook: %s: read: %w", path, err)
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("skyhook: %s: decode: %w", path, err)
	}
	return nil
}
