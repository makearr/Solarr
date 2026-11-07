package arrimport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

// AddDefaults are what an item is added under: the *Arr's own quality profile, metadata profile (Lidarr only) and root folder.
type AddDefaults struct {
	QualityProfileID  int64
	MetadataProfileID int64
	RootFolderPath    string
}

// lookup answers the resources an *Arr's lookup finds for a term; one already in the library carries its id.
func (c LidarrClient) lookup(ctx context.Context, path, term string) ([]map[string]any, error) {
	var found []map[string]any
	err := c.get(ctx, path+"?term="+url.QueryEscape(term), &found)
	return found, err
}

// idOf reads the *Arr's id off a lookup result, zero when the item is not in its library.
func idOf(resource map[string]any) int64 {
	id, _ := resource["id"].(float64)
	return int64(id)
}

// MetadataProfiles answers Lidarr's metadata profiles.
func (c LidarrClient) MetadataProfiles(ctx context.Context) ([]LidarrQualityProfile, error) {
	var profiles []LidarrQualityProfile
	err := c.get(ctx, "/api/v1/metadataprofile", &profiles)
	return profiles, err
}

// LookupArtist finds an artist by its MusicBrainz id; the id is Lidarr's when it already holds it.
func (c LidarrClient) LookupArtist(ctx context.Context, mbid string) (map[string]any, int64, bool, error) {
	found, err := c.lookup(ctx, "/api/v1/artist/lookup", "lidarr:"+mbid)
	if err != nil || len(found) == 0 {
		return nil, 0, false, err
	}
	return found[0], idOf(found[0]), true, nil
}

// AddArtist adds the looked-up artist monitored and has Lidarr search for its albums; it answers Lidarr's id.
func (c LidarrClient) AddArtist(ctx context.Context, artist map[string]any, defaults AddDefaults) (int64, error) {
	delete(artist, "id")
	artist["qualityProfileId"], artist["metadataProfileId"] = defaults.QualityProfileID, defaults.MetadataProfileID
	artist["rootFolderPath"], artist["monitored"], artist["monitorNewItems"] = defaults.RootFolderPath, true, "all"
	artist["addOptions"] = map[string]any{"monitor": "all", "searchForMissingAlbums": true}
	body, err := c.post(ctx, "/api/v1/artist", artist)
	if err != nil {
		return 0, err
	}
	return created(body)
}

// SearchArtist has Lidarr search for the missing albums of an artist it already holds.
func (c LidarrClient) SearchArtist(ctx context.Context, id int64) error {
	return c.command(ctx, "/api/v1/command", map[string]any{"name": "ArtistSearch", "artistId": id})
}

// LookupAlbum finds an album by its MusicBrainz release group id; the id is Lidarr's when it already holds it.
func (c LidarrClient) LookupAlbum(ctx context.Context, mbid string) (map[string]any, int64, bool, error) {
	found, err := c.lookup(ctx, "/api/v1/album/lookup", "lidarr:"+mbid)
	if err != nil || len(found) == 0 {
		return nil, 0, false, err
	}
	return found[0], idOf(found[0]), true, nil
}

// AddAlbum adds the looked-up album, and its artist when Lidarr lacks it, monitored with a search for it; it answers Lidarr's id.
func (c LidarrClient) AddAlbum(ctx context.Context, album map[string]any, defaults AddDefaults) (int64, error) {
	delete(album, "id")
	artist, _ := album["artist"].(map[string]any)
	if artist == nil {
		artist = map[string]any{}
	}
	delete(artist, "id")
	artist["qualityProfileId"], artist["metadataProfileId"] = defaults.QualityProfileID, defaults.MetadataProfileID
	artist["rootFolderPath"], artist["monitored"], artist["monitorNewItems"] = defaults.RootFolderPath, true, "none"
	album["artist"], album["monitored"], album["anyReleaseOk"] = artist, true, true
	album["addOptions"] = map[string]any{"addType": "automatic", "searchForNewAlbum": true}
	body, err := c.post(ctx, "/api/v1/album", album)
	if err != nil {
		return 0, err
	}
	return created(body)
}

// SearchAlbum has Lidarr search for an album it already holds.
func (c LidarrClient) SearchAlbum(ctx context.Context, id int64) error {
	return c.command(ctx, "/api/v1/command", map[string]any{"name": "AlbumSearch", "albumIds": []int64{id}})
}

// LookupScenes finds Whisparr's scenes for a title; Whisparr cannot look one up by its id. A scene it already holds carries its id.
func (c WhisparrClient) LookupScenes(ctx context.Context, title string) ([]map[string]any, error) {
	return c.inner.lookup(ctx, "/api/v3/movie/lookup", title)
}

// AddScene adds the looked-up scene monitored and has Whisparr search for it; it answers Whisparr's id.
func (c WhisparrClient) AddScene(ctx context.Context, scene map[string]any, defaults AddDefaults) (int64, error) {
	delete(scene, "id")
	scene["qualityProfileId"], scene["rootFolderPath"], scene["monitored"] = defaults.QualityProfileID, defaults.RootFolderPath, true
	scene["addOptions"] = map[string]any{"searchForMovie": true}
	body, err := c.inner.post(ctx, "/api/v3/movie", scene)
	if err != nil {
		return 0, err
	}
	return created(body)
}

// SearchScene has Whisparr search for a scene it already holds.
func (c WhisparrClient) SearchScene(ctx context.Context, id int64) error {
	return c.inner.command(ctx, "/api/v3/command", map[string]any{"name": "MoviesSearch", "movieIds": []int64{id}})
}
