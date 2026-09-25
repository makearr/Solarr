// Package theaudiodb reads TheAudioDB (theaudiodb.com): artist photos, logos, fanart and album
// covers, looked up by MusicBrainz id. The public key works without registering; a supporter's
// own key replaces it (answered 2026-09-25).
package theaudiodb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

// PublicKey is TheAudioDB's free key, verified against the live API 2026-09-25.
const PublicKey = "123"

// ErrNotFound means TheAudioDB knows nothing for that id.
var ErrNotFound = errors.New("theaudiodb: not found")

const (
	baseURL          = "https://www.theaudiodb.com/api/v1/json/"
	maxResponseBytes = 2 << 20
)

// Artist is what TheAudioDB has for one artist; any field may be empty.
type Artist struct {
	Photo     string
	Fanart    string
	Logo      string
	Biography string
}

// Client asks TheAudioDB. Key answers the key to use, read per request so a new one takes effect.
type Client struct {
	http *httpx.Client
	key  func() string
}

// New builds a client; a nil key func uses PublicKey.
func New(client *httpx.Client, key func() string) Client {
	if key == nil {
		key = func() string { return PublicKey }
	}
	return Client{http: client, key: key}
}

// Artist looks one artist up by MusicBrainz artist id.
func (c Client) Artist(ctx context.Context, artistMBID string) (Artist, error) {
	var parsed struct {
		Artists []struct {
			Thumb     string `json:"strArtistThumb"`
			Fanart    string `json:"strArtistFanart"`
			Logo      string `json:"strArtistLogo"`
			Biography string `json:"strBiographyEN"`
		} `json:"artists"`
	}
	if err := c.get(ctx, "artist-mb.php", artistMBID, &parsed); err != nil {
		return Artist{}, err
	}
	if len(parsed.Artists) == 0 {
		return Artist{}, ErrNotFound
	}
	found := parsed.Artists[0]
	return Artist{Photo: found.Thumb, Fanart: found.Fanart, Logo: found.Logo, Biography: found.Biography}, nil
}

// AlbumCover answers the cover image URL of one release group, by its MusicBrainz id.
func (c Client) AlbumCover(ctx context.Context, releaseGroupMBID string) (string, error) {
	var parsed struct {
		Album []struct {
			Thumb string `json:"strAlbumThumb"`
		} `json:"album"`
	}
	if err := c.get(ctx, "album-mb.php", releaseGroupMBID, &parsed); err != nil {
		return "", err
	}
	if len(parsed.Album) == 0 || parsed.Album[0].Thumb == "" {
		return "", ErrNotFound
	}
	return parsed.Album[0].Thumb, nil
}

// get fetches one lookup. The key sits in the URL's path, where httpx cannot redact it, so no
// error from the transport is passed on with it in.
func (c Client) get(ctx context.Context, endpoint, mbid string, into any) error {
	key := c.key()
	requestURL := baseURL + url.PathEscape(key) + "/" + endpoint + "?i=" + url.QueryEscape(mbid)
	response, err := c.http.Get(ctx, requestURL)
	if err != nil {
		return fmt.Errorf("theaudiodb: %s: %s", endpoint, strings.ReplaceAll(err.Error(), key, "***"))
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("theaudiodb: %s: status %d", endpoint, response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("theaudiodb: %s: read: %w", endpoint, err)
	}
	// An unknown id answers {"artists":null}, which decodes to nothing found.
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("theaudiodb: %s: decode: %w", endpoint, err)
	}
	return nil
}
