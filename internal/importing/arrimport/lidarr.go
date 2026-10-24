// Package arrimport is ADR-52's own home: one REST client per *Arr
// application, over its documented API and an API key only -- no database
// reading, no file access, no writes back to the source. Only Lidarr is
// fully wired at stage 4 (music exists); the client shapes for the other
// four apps land with their own "connect, preview, park" support.
package arrimport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

// maxLidarrResponseBytes bounds what one call can make this process read.
// 128 MiB, not the 16 MiB musicbrainz's own per-release lookups use:
// Lidarr's own Album objects carry far more than this package's minimal
// fields read, and one real prolific artist's own Albums response measured
// 28 MiB for 2,050 albums live -- a misbehaving or malicious instance
// answering more than this is still refused rather than read without limit.
const maxLidarrResponseBytes = 128 << 20

// ErrResponseTooLarge reports a response this package refused to finish
// reading past maxLidarrResponseBytes.
var ErrResponseTooLarge = errors.New("arrimport: response exceeded the size limit")

// LidarrStatus is GET /api/v1/system/status's own shape, trimmed to what a
// connection test needs.
type LidarrStatus struct {
	Version string `json:"version"`
}

// LidarrRootFolder is one of Lidarr's own configured root folders --
// ADR-52's "root folders are matched by path against roots Solarr
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
	Statistics     struct {
		TrackFileCount  int64 `json:"trackFileCount"`
		TotalTrackCount int64 `json:"totalTrackCount"`
	} `json:"statistics"`
}

// Complete is an album Lidarr holds every track of.
func (a LidarrAlbum) Complete() bool {
	return a.Statistics.TotalTrackCount > 0 && a.Statistics.TrackFileCount >= a.Statistics.TotalTrackCount
}

// LidarrQualityProfile is GET /api/v1/qualityprofile's own shape -- report
// only (ADR-52: "custom formats do not translate either... imported as a
// report of what existed"), the same treatment applied here since no
// Solarr profile-creation UI exists yet to propose a mapping into
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

// DownloadClient is one download client an *Arr instance uses, from /api/v1/downloadclient -- the
// v1 shape Lidarr and Prowlarr share. Its secrets come back masked, so none is read.
type DownloadClient struct {
	Name           string
	Implementation string
	Protocol       string
	Enabled        bool
	Host           string
	Port           int64
	UseSSL         bool
	URLBase        string
	Category       string
	// Username is where the *Arr keeps one (qBittorrent); the password comes back masked, so none is read.
	Username string
}

type downloadClientWire struct {
	Name           string `json:"name"`
	Implementation string `json:"implementation"`
	Protocol       string `json:"protocol"`
	Enable         bool   `json:"enable"`
	Fields         []struct {
		Name  string          `json:"name"`
		Value json.RawMessage `json:"value"`
	} `json:"fields"`
}

// DownloadClients answers the instance's download clients, their address and category.
func (c LidarrClient) DownloadClients(ctx context.Context) ([]DownloadClient, error) {
	return c.downloadClients(ctx, "/api/v1/downloadclient")
}

// downloadClients reads one *Arr's download clients from its own API version's path; the field shapes are shared.
func (c LidarrClient) downloadClients(ctx context.Context, path string) ([]DownloadClient, error) {
	var wire []downloadClientWire
	if err := c.get(ctx, path, &wire); err != nil {
		return nil, err
	}
	clients := make([]DownloadClient, len(wire))
	for index, entry := range wire {
		client := DownloadClient{
			Name: entry.Name, Implementation: entry.Implementation, Protocol: entry.Protocol, Enabled: entry.Enable,
		}
		for _, field := range entry.Fields {
			decode := func(target any) error {
				// An *Arr leaves value out for an unset field: nothing to read, not a malformed answer.
				if len(field.Value) == 0 {
					return nil
				}
				if err := json.Unmarshal(field.Value, target); err != nil {
					return fmt.Errorf("arrimport: download client %q field %q: %w", entry.Name, field.Name, err)
				}
				return nil
			}
			switch field.Name {
			case "host":
				if err := decode(&client.Host); err != nil {
					return nil, err
				}
			case "port":
				if err := decode(&client.Port); err != nil {
					return nil, err
				}
			case "useSsl":
				if err := decode(&client.UseSSL); err != nil {
					return nil, err
				}
			case "urlBase":
				if err := decode(&client.URLBase); err != nil {
					return nil, err
				}
			case "username":
				if err := decode(&client.Username); err != nil {
					return nil, err
				}
			case "category", "musicCategory", "tvCategory", "movieCategory":
				var category string
				if err := decode(&category); err != nil {
					return nil, err
				}
				if category != "" && client.Category == "" {
					client.Category = category
				}
			}
		}
		clients[index] = client
	}
	return clients, nil
}

// BaseURL is the client's address as a URL: scheme, host, port and its URL base.
func (d DownloadClient) BaseURL() string {
	scheme := "http"
	if d.UseSSL {
		scheme = "https"
	}
	base := strings.Trim(d.URLBase, "/")
	address := scheme + "://" + net.JoinHostPort(strings.Trim(d.Host, "[]"), strconv.FormatInt(d.Port, 10))
	if base != "" {
		address += "/" + base
	}
	return address
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

	// Read one byte past the limit so a response that exactly fills it is
	// reported honestly (ErrResponseTooLarge) instead of silently truncated
	// into a confusing "unexpected end of JSON input" from Unmarshal --
	// found live against a real prolific artist's own Albums response
	// (2,050 albums, 28 MiB: Lidarr's own album objects carry much more than
	// this package's minimal fields read, nothing like musicbrainz's own
	// per-object size).
	body, err := io.ReadAll(io.LimitReader(response.Body, maxLidarrResponseBytes+1))
	if err != nil {
		return fmt.Errorf("arrimport: read response for %s: %w", path, err)
	}
	if len(body) > maxLidarrResponseBytes {
		return fmt.Errorf("arrimport: %s: %w (over %d bytes)", path, ErrResponseTooLarge, maxLidarrResponseBytes)
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("arrimport: decode response for %s: %w", path, err)
	}
	return nil
}
