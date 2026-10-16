package arrimport

import (
	"context"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

// RadarrMovie is GET /api/v3/movie trimmed to what an import maps: the TMDB id is the identity.
type RadarrMovie struct {
	ID                  int64   `json:"id"`
	Title               string  `json:"title"`
	TMDBID              int64   `json:"tmdbId"`
	IMDbID              string  `json:"imdbId"`
	Monitored           bool    `json:"monitored"`
	MinimumAvailability string  `json:"minimumAvailability"`
	RootFolderPath      string  `json:"rootFolderPath"`
	QualityProfileID    int64   `json:"qualityProfileId"`
	HasFile             bool    `json:"hasFile"`
	Tags                []int64 `json:"tags"`
}

// RadarrClient is the REST client for one Radarr v3/v4/v5 instance: GET only, API key only (ADR-52).
type RadarrClient struct{ inner LidarrClient }

// Radarr reads the same instance as a Radarr: the transport and key are shared, only the API version differs.
func (c LidarrClient) Radarr() RadarrClient { return RadarrClient{inner: c} }

// NewRadarrClient builds one over an already-configured httpx.Client.
func NewRadarrClient(client *httpx.Client, baseURL, apiKey string) RadarrClient {
	return RadarrClient{inner: NewLidarrClient(client, baseURL, apiKey)}
}

// SystemStatus is the connection test.
func (c RadarrClient) SystemStatus(ctx context.Context) (LidarrStatus, error) {
	var status LidarrStatus
	if err := c.inner.get(ctx, "/api/v3/system/status", &status); err != nil {
		return LidarrStatus{}, err
	}
	return status, nil
}

// RootFolders answers the instance's root folders.
func (c RadarrClient) RootFolders(ctx context.Context) ([]LidarrRootFolder, error) {
	var folders []LidarrRootFolder
	if err := c.inner.get(ctx, "/api/v3/rootfolder", &folders); err != nil {
		return nil, err
	}
	return folders, nil
}

// Movies answers every movie in the instance.
func (c RadarrClient) Movies(ctx context.Context) ([]RadarrMovie, error) {
	var movies []RadarrMovie
	if err := c.inner.get(ctx, "/api/v3/movie", &movies); err != nil {
		return nil, err
	}
	return movies, nil
}

// QualityProfiles answers the instance's quality profiles, so a movie's profile id can be read as a name.
func (c RadarrClient) QualityProfiles(ctx context.Context) ([]SonarrQualityProfile, error) {
	var profiles []SonarrQualityProfile
	if err := c.inner.get(ctx, "/api/v3/qualityprofile", &profiles); err != nil {
		return nil, err
	}
	return profiles, nil
}
