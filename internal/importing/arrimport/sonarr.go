package arrimport

import (
	"context"
	"strconv"

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

// SonarrEpisode is one episode's monitored flag and whether Sonarr has a file for it.
type SonarrEpisode struct {
	SeasonNumber  int  `json:"seasonNumber"`
	EpisodeNumber int  `json:"episodeNumber"`
	Monitored     bool `json:"monitored"`
	HasFile       bool `json:"hasFile"`
}

// Episodes answers every episode of one series, with the monitoring Sonarr holds for each.
func (c SonarrClient) Episodes(ctx context.Context, seriesID int64) ([]SonarrEpisode, error) {
	var episodes []SonarrEpisode
	if err := c.inner.get(ctx, "/api/v3/episode?seriesId="+strconv.FormatInt(seriesID, 10), &episodes); err != nil {
		return nil, err
	}
	return episodes, nil
}

// SonarrQualityProfile is a quality profile's id and name.
type SonarrQualityProfile struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// QualityProfiles answers the instance's quality profiles, so a series' profile id can be read as a name.
func (c SonarrClient) QualityProfiles(ctx context.Context) ([]SonarrQualityProfile, error) {
	var profiles []SonarrQualityProfile
	if err := c.inner.get(ctx, "/api/v3/qualityprofile", &profiles); err != nil {
		return nil, err
	}
	return profiles, nil
}
