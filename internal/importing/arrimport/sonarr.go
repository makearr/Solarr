package arrimport

import (
	"context"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

// SonarrSeason is one season's monitored flag as Sonarr holds it.
type SonarrSeason struct {
	SeasonNumber int  `json:"seasonNumber"`
	Monitored    bool `json:"monitored"`
}

// SonarrSeries is GET /api/v3/series trimmed to what an import maps: the TVDB id is the identity.
type SonarrSeries struct {
	ID               int64          `json:"id"`
	Title            string         `json:"title"`
	TVDBID           int64          `json:"tvdbId"`
	TVMazeID         int64          `json:"tvMazeId"`
	TMDBID           int64          `json:"tmdbId"`
	IMDbID           string         `json:"imdbId"`
	Monitored        bool           `json:"monitored"`
	SeriesType       string         `json:"seriesType"`
	RootFolderPath   string         `json:"rootFolderPath"`
	QualityProfileID int64          `json:"qualityProfileId"`
	Seasons          []SonarrSeason `json:"seasons"`
	Tags             []int64        `json:"tags"`
}

// Sonarr reads the same instance as a Sonarr: the transport and key are shared, only the API version differs.
func (c LidarrClient) Sonarr() SonarrClient { return SonarrClient{inner: c} }

// SonarrClient is the REST client for one Sonarr v3/v4 instance: GET only, API key only (ADR-52).
type SonarrClient struct{ inner LidarrClient }

// NewSonarrClient builds one over an already-configured httpx.Client.
func NewSonarrClient(client *httpx.Client, baseURL, apiKey string) SonarrClient {
	return SonarrClient{inner: NewLidarrClient(client, baseURL, apiKey)}
}

// SystemStatus is the connection test.
func (c SonarrClient) SystemStatus(ctx context.Context) (LidarrStatus, error) {
	var status LidarrStatus
	if err := c.inner.get(ctx, "/api/v3/system/status", &status); err != nil {
		return LidarrStatus{}, err
	}
	return status, nil
}

// RootFolders answers the instance's root folders.
func (c SonarrClient) RootFolders(ctx context.Context) ([]LidarrRootFolder, error) {
	var folders []LidarrRootFolder
	if err := c.inner.get(ctx, "/api/v3/rootfolder", &folders); err != nil {
		return nil, err
	}
	return folders, nil
}

// Series answers every series in the instance.
func (c SonarrClient) Series(ctx context.Context) ([]SonarrSeries, error) {
	var series []SonarrSeries
	if err := c.inner.get(ctx, "/api/v3/series", &series); err != nil {
		return nil, err
	}
	return series, nil
}
