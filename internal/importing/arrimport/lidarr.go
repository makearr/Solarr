// Package arrimport is ADR-52's own home: one REST client per *Arr
// application, over its documented API and an API key only -- no database
// reading, no file access, no writes back to the source. Only Lidarr is
// fully wired at stage 4 (music exists); the client shapes for the other
// four apps land with their own "connect, preview, park" support.
package arrimport

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/makearr/MediaNexus/internal/platform/httpx"
)

// maxLidarrResponseBytes bounds what one call can make this process read --
// a full artist or album listing for even a large library is well under
// this; a misbehaving or malicious instance answering more is refused
// rather than read without limit, the same reasoning musicbrainz's own
// maxResponseBytes states.
const maxLidarrResponseBytes = 16 << 20

// LidarrStatus is GET /api/v1/system/status's own shape, trimmed to what a
// connection test needs.
type LidarrStatus struct {
	Version string `json:"version"`
}

// LidarrRootFolder is one of Lidarr's own configured root folders --
// ADR-52's "root folders are matched by path against roots MediaNexus
// already knows".
type LidarrRootFolder struct {
	ID   int64  `json:"id"`
	Path string `json:"path"`
}

// LidarrArtist is GET /api/v1/artist's own shape, trimmed to what todo
// 4.13 imports. ForeignArtistID is Lidarr's own field name for the artist's
// MusicBrainz id -- ADR-8's own external identity, reused rather than
// inventing a parallel one.
type LidarrArtist struct {
	ID               int64   `json:"id"`
	ArtistName       string  `json:"artistName"`
	SortName         string  `json:"sortName"`
	ForeignArtistID  string  `json:"foreignArtistId"`
	Monitored        bool    `json:"monitored"`
	RootFolderPath   string  `json:"rootFolderPath"`
	QualityProfileID int64   `json:"qualityProfileId"`
	Tags             []int64 `json:"tags"`
}

// LidarrAlbum is GET /api/v1/album's own shape, trimmed to what todo 4.13
// imports. ForeignAlbumID is Lidarr's own field name for the release
// group's MusicBrainz id.
type LidarrAlbum struct {
	ID             int64  `json:"id"`
	Title          string `json:"title"`
	ArtistID       int64  `json:"artistId"`
	ForeignAlbumID string `json:"foreignAlbumId"`
	Monitored      bool   `json:"monitored"`
}

// LidarrQualityProfile is GET /api/v1/qualityprofile's own shape -- report
// only (ADR-52: "custom formats do not translate either... imported as a
// report of what existed"), the same treatment applied here since no
// MediaNexus profile-creation UI exists yet to propose a mapping into
// (todo 4.3).
type LidarrQualityProfile struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// LidarrTag is GET /api/v1/tag's own shape.
type LidarrTag struct {
	ID    int64  `json:"id"`
	Label string `json:"label"`
}

// LidarrClient is a thin REST client over one Lidarr instance's own
// documented v1 API. API key only, sent as X-Api-Key -- no writes back to
// the source (ADR-52).
type LidarrClient struct {
	client  *httpx.Client
	baseURL string
	apiKey  string
}

// NewLidarrClient builds one over an already-configured httpx.Client (its
// own rate limiter/breaker/proxy policy, ADR-13/ADR-58 -- this package has
// no opinion on any of that).
func NewLidarrClient(client *httpx.Client, baseURL, apiKey string) LidarrClient {
	return LidarrClient{client: client, baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey}
}

// SystemStatus answers the instance's own version -- ADR-52's connection
// test: a wrong base URL or API key fails here, before anything else asks it.
func (c LidarrClient) SystemStatus(ctx context.Context) (LidarrStatus, error) {
	var status LidarrStatus
	if err := c.get(ctx, "/api/v1/system/status", &status); err != nil {
		return LidarrStatus{}, err
	}
	return status, nil
}

// RootFolders answers every root folder Lidarr itself knows about.
func (c LidarrClient) RootFolders(ctx context.Context) ([]LidarrRootFolder, error) {
	var folders []LidarrRootFolder
	if err := c.get(ctx, "/api/v1/rootfolder", &folders); err != nil {
		return nil, err
	}
	return folders, nil
}

// Artists answers every artist in the instance's own library.
func (c LidarrClient) Artists(ctx context.Context) ([]LidarrArtist, error) {
	var artists []LidarrArtist
	if err := c.get(ctx, "/api/v1/artist", &artists); err != nil {
		return nil, err
	}
	return artists, nil
}

// Albums answers every album for one artist.
func (c LidarrClient) Albums(ctx context.Context, artistID int64) ([]LidarrAlbum, error) {
	var albums []LidarrAlbum
	if err := c.get(ctx, "/api/v1/album?artistId="+strconv.FormatInt(artistID, 10), &albums); err != nil {
		return nil, err
	}
	return albums, nil
}

// QualityProfiles answers every quality profile configured on the instance
// -- a report only (this type's own doc comment).
func (c LidarrClient) QualityProfiles(ctx context.Context) ([]LidarrQualityProfile, error) {
	var profiles []LidarrQualityProfile
	if err := c.get(ctx, "/api/v1/qualityprofile", &profiles); err != nil {
		return nil, err
	}
	return profiles, nil
}

// Tags answers every tag configured on the instance.
func (c LidarrClient) Tags(ctx context.Context) ([]LidarrTag, error) {
	var tags []LidarrTag
	if err := c.get(ctx, "/api/v1/tag", &tags); err != nil {
		return nil, err
	}
	return tags, nil
}

// get is the common request shape every call above uses: GET with the API
// key header, bounded read, decode. httpx.Client.Do already classifies a
// non-2xx response into a *httpx.StatusError (wrong API key answers 401,
// caught here the same as any other non-success status).
func (c LidarrClient) get(ctx context.Context, path string, into any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("arrimport: build request for %s: %w", path, err)
	}
	request.Header.Set("X-Api-Key", c.apiKey)

	response, err := c.client.Do(ctx, request)
	if err != nil {
		return fmt.Errorf("arrimport: %s: %w", path, err)
	}
	defer func() { _ = response.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(response.Body, maxLidarrResponseBytes))
	if err != nil {
		return fmt.Errorf("arrimport: read response for %s: %w", path, err)
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("arrimport: decode response for %s: %w", path, err)
	}
	return nil
}
