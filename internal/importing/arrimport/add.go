package arrimport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Adding an item to an *Arr and asking it to search are the only writes beside SetMonitored, made when Solarr runs as
// an *Arr's companion (documentation/solarr-mode.md): the *Arr, not Solarr, then searches and downloads.

// post sends a JSON body with the API key and answers the response body.
func (c LidarrClient) post(ctx context.Context, path string, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("arrimport: build request for %s: %w", path, err)
	}
	request.Header.Set("X-Api-Key", c.apiKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := c.client.Do(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("arrimport: POST %s: %w", path, err)
	}
	defer func() { _ = response.Body.Close() }()
	return io.ReadAll(io.LimitReader(response.Body, 1<<20))
}

// command starts one of the *Arr's own commands, such as a search.
func (c LidarrClient) command(ctx context.Context, path string, payload map[string]any) error {
	_, err := c.post(ctx, path, payload)
	return err
}

// created reads the id of the resource a POST answered with.
func created(body []byte) (int64, error) {
	var resource struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(body, &resource); err != nil || resource.ID == 0 {
		return 0, fmt.Errorf("arrimport: the *Arr did not answer with the id of what it added")
	}
	return resource.ID, nil
}

// RadarrAdd is a movie to add: the TMDB id is the identity, the profile and folder are the *Arr's own.
type RadarrAdd struct {
	Title            string
	TMDBID           int64
	QualityProfileID int64
	RootFolderPath   string
}

// AddMovie adds a monitored movie and has Radarr search for it; it answers Radarr's id of it.
func (c RadarrClient) AddMovie(ctx context.Context, movie RadarrAdd) (int64, error) {
	body, err := c.inner.post(ctx, "/api/v3/movie", map[string]any{
		"title": movie.Title, "tmdbId": movie.TMDBID, "qualityProfileId": movie.QualityProfileID,
		"rootFolderPath": movie.RootFolderPath, "monitored": true, "minimumAvailability": "released",
		"addOptions": map[string]any{"searchForMovie": true},
	})
	if err != nil {
		return 0, err
	}
	return created(body)
}

// SearchMovie has Radarr search for a movie it already holds.
func (c RadarrClient) SearchMovie(ctx context.Context, id int64) error {
	return c.inner.command(ctx, "/api/v3/command", map[string]any{"name": "MoviesSearch", "movieIds": []int64{id}})
}

// SonarrAdd is a series to add: the TVDB id is the identity.
type SonarrAdd struct {
	Title             string
	TVDBID            int64
	QualityProfileID  int64
	LanguageProfileID int64
	RootFolderPath    string
}

// AddSeries adds a monitored series and has Sonarr search for its missing episodes; it answers Sonarr's id of it.
func (c SonarrClient) AddSeries(ctx context.Context, series SonarrAdd) (int64, error) {
	payload := map[string]any{
		"title": series.Title, "tvdbId": series.TVDBID, "qualityProfileId": series.QualityProfileID,
		"rootFolderPath": series.RootFolderPath, "monitored": true, "seasonFolder": true, "seriesType": "standard",
		"addOptions": map[string]any{"monitor": "all", "searchForMissingEpisodes": true},
	}
	if series.LanguageProfileID != 0 {
		payload["languageProfileId"] = series.LanguageProfileID
	}
	body, err := c.inner.post(ctx, "/api/v3/series", payload)
	if err != nil {
		return 0, err
	}
	return created(body)
}

// SearchSeries has Sonarr search for the missing episodes of a series it already holds.
func (c SonarrClient) SearchSeries(ctx context.Context, id int64) error {
	return c.inner.command(ctx, "/api/v3/command", map[string]any{"name": "SeriesSearch", "seriesId": id})
}
