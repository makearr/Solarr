// Package listenbrainz reads ListenBrainz (no key): how often a release group was listened to, the site's weekly charts and its fresh releases.
package listenbrainz

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

const (
	baseURL          = "https://api.listenbrainz.org/1"
	address          = baseURL + "/popularity/release-group"
	maxResponseBytes = 1 << 20
	// maxChartBytes bounds a chart answer: the fresh releases of a month come in one answer of a few thousand rows.
	maxChartBytes = 8 << 20
)

// ErrNotFound means ListenBrainz holds no listens for the release group.
var ErrNotFound = errors.New("listenbrainz: no listens")

// Doer is the httpx client.
type Doer interface {
	Do(ctx context.Context, request *http.Request) (*http.Response, error)
}

// Client asks ListenBrainz.
type Client struct{ http Doer }

// New builds a client.
func New(client Doer) Client { return Client{http: client} }

// Popularity is a release group's total listens and the listeners behind them.
type Popularity struct{ Listens, Listeners int64 }

type wireRow struct {
	Listens   *int64 `json:"total_listen_count"`
	Listeners *int64 `json:"total_user_count"`
}

// ReleaseGroup answers the popularity of a MusicBrainz release group; ListenBrainz sends nulls for one it has not seen.
func (c Client) ReleaseGroup(ctx context.Context, mbid string) (Popularity, error) {
	body, err := json.Marshal(map[string][]string{"release_group_mbids": {mbid}})
	if err != nil {
		return Popularity{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, address, bytes.NewReader(body))
	if err != nil {
		return Popularity{}, fmt.Errorf("listenbrainz: build request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(ctx, request)
	if err != nil {
		return Popularity{}, fmt.Errorf("listenbrainz: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return Popularity{}, fmt.Errorf("listenbrainz: status %d", response.StatusCode)
	}
	data, err := httpx.ReadLimited(response.Body, maxResponseBytes)
	if err != nil {
		return Popularity{}, fmt.Errorf("listenbrainz: read: %w", err)
	}
	var rows []wireRow
	if err := json.Unmarshal(data, &rows); err != nil {
		return Popularity{}, fmt.Errorf("listenbrainz: decode: %w", err)
	}
	if len(rows) == 0 || rows[0].Listens == nil || *rows[0].Listens <= 0 {
		return Popularity{}, ErrNotFound
	}
	found := Popularity{Listens: *rows[0].Listens}
	if rows[0].Listeners != nil {
		found.Listeners = *rows[0].Listeners
	}
	return found, nil
}

// The ranges a sitewide chart covers.
const (
	RangeWeek    = "week"
	RangeAllTime = "all_time"
)

// ChartArtist is one row of the sitewide artist chart.
type ChartArtist struct {
	MBID    string `json:"artist_mbid"`
	Name    string `json:"artist_name"`
	Listens int64  `json:"listen_count"`
}

// ChartReleaseGroup is one row of the sitewide release group chart.
type ChartReleaseGroup struct {
	MBID         string   `json:"release_group_mbid"`
	Title        string   `json:"release_group_name"`
	Artist       string   `json:"artist_name"`
	ArtistMBIDs  []string `json:"artist_mbids"`
	CoverRelease string   `json:"caa_release_mbid"`
	Listens      int64    `json:"listen_count"`
}

// FreshRelease is one release of the site's release calendar.
type FreshRelease struct {
	ReleaseGroupMBID string   `json:"release_group_mbid"`
	Title            string   `json:"release_name"`
	Artist           string   `json:"artist_credit_name"`
	ArtistMBIDs      []string `json:"artist_mbids"`
	CoverRelease     string   `json:"caa_release_mbid"`
	ReleasedOn       string   `json:"release_date"`
	PrimaryType      string   `json:"release_group_primary_type"`
}

// SitewideArtists reads one page of the most listened artists over a range; total is the chart's length.
func (c Client) SitewideArtists(ctx context.Context, period string, offset, count int) (rows []ChartArtist, total int, err error) {
	var answer struct {
		Payload struct {
			Artists []ChartArtist `json:"artists"`
			Total   int           `json:"total_artist_count"`
		} `json:"payload"`
	}
	if err := c.chart(ctx, "/stats/sitewide/artists", period, offset, count, &answer); err != nil {
		return nil, 0, err
	}
	return answer.Payload.Artists, answer.Payload.Total, nil
}

// SitewideReleaseGroups reads one page of the most listened release groups over a range; total is the chart's length.
func (c Client) SitewideReleaseGroups(ctx context.Context, period string, offset, count int) (rows []ChartReleaseGroup, total int, err error) {
	var answer struct {
		Payload struct {
			ReleaseGroups []ChartReleaseGroup `json:"release_groups"`
			Total         int                 `json:"total_release_group_count"`
		} `json:"payload"`
	}
	if err := c.chart(ctx, "/stats/sitewide/release-groups", period, offset, count, &answer); err != nil {
		return nil, 0, err
	}
	return answer.Payload.ReleaseGroups, answer.Payload.Total, nil
}

// UpcomingReleases reads the releases due within the next days, soonest first.
func (c Client) UpcomingReleases(ctx context.Context, days int) ([]FreshRelease, error) {
	query := url.Values{"days": {strconv.Itoa(days)}, "past": {"false"}, "future": {"true"}, "sort": {"release_date"}}
	var answer struct {
		Payload struct {
			Releases []FreshRelease `json:"releases"`
		} `json:"payload"`
	}
	if err := c.get(ctx, baseURL+"/explore/fresh-releases/?"+query.Encode(), &answer); err != nil {
		return nil, err
	}
	return answer.Payload.Releases, nil
}

func (c Client) chart(ctx context.Context, path, period string, offset, count int, into any) error {
	query := url.Values{"range": {period}, "offset": {strconv.Itoa(offset)}, "count": {strconv.Itoa(count)}}
	return c.get(ctx, baseURL+path+"?"+query.Encode(), into)
}

func (c Client) get(ctx context.Context, address string, into any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return fmt.Errorf("listenbrainz: build request: %w", err)
	}
	response, err := c.http.Do(ctx, request)
	if err != nil {
		return fmt.Errorf("listenbrainz: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusNoContent {
		return nil
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("listenbrainz: status %d", response.StatusCode)
	}
	data, err := httpx.ReadLimited(response.Body, maxChartBytes)
	if err != nil {
		return fmt.Errorf("listenbrainz: read: %w", err)
	}
	if err := json.Unmarshal(data, into); err != nil {
		return fmt.Errorf("listenbrainz: decode: %w", err)
	}
	return nil
}
