// Package flood is the REST client of Flood behind download.TorrentClient (ADR-46): Solarr signs in for Flood's jwt
// cookie, and a torrent's category is a Flood tag.
package flood

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

// maxResponseBytes bounds what one call can make this process read.
const maxResponseBytes = 8 << 20

// ErrLoginRefused is Flood refusing the user name or password.
var ErrLoginRefused = errors.New("flood: the user name or password was refused")

// Client is a thin REST client over one connection; credentials are "user:password".
type Client struct {
	http     *httpx.Client
	apiURL   string
	username string
	password string
	session  *string
}

// NewClient builds one over an already-configured httpx.Client.
func NewClient(client *httpx.Client, baseURL, credentials string) Client {
	user, password, _ := strings.Cut(credentials, ":")
	return Client{http: client, apiURL: strings.TrimRight(baseURL, "/") + "/api", username: user, password: password, session: new(string)}
}

func (c Client) send(ctx context.Context, method, path string, payload any) (*http.Response, error) {
	var body []byte
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = encoded
	}
	request, err := http.NewRequestWithContext(ctx, method, c.apiURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if *c.session != "" {
		request.AddCookie(&http.Cookie{Name: "jwt", Value: *c.session})
	}
	return c.http.Do(ctx, request)
}

func (c Client) login(ctx context.Context) error {
	response, err := c.send(ctx, http.MethodPost, "/auth/authenticate", map[string]string{"username": c.username, "password": c.password})
	var refused *httpx.StatusError
	if errors.As(err, &refused) && refused.Status == http.StatusUnauthorized {
		return ErrLoginRefused
	}
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	for _, cookie := range response.Cookies() {
		if cookie.Name == "jwt" {
			*c.session = cookie.Value
		}
	}
	return nil
}

// call performs one request, signing in first and again once when Flood answers 401.
func (c Client) call(ctx context.Context, method, path string, payload any, into any) error {
	if *c.session == "" {
		if err := c.login(ctx); err != nil {
			return err
		}
	}
	for attempt := 0; ; attempt++ {
		response, err := c.send(ctx, method, path, payload)
		var expired *httpx.StatusError
		if errors.As(err, &expired) && expired.Status == http.StatusUnauthorized && attempt == 0 {
			if err := c.login(ctx); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		body, err := httpx.ReadLimited(response.Body, maxResponseBytes)
		_ = response.Body.Close()
		if err != nil {
			return fmt.Errorf("flood: read %s: %w", path, err)
		}
		if into == nil {
			return nil
		}
		if err := json.Unmarshal(body, into); err != nil {
			return fmt.Errorf("flood: decode %s: %w", path, err)
		}
		return nil
	}
}

// ConnectionTest answers whether Flood is connected to its torrent client.
func (c Client) ConnectionTest(ctx context.Context) (bool, error) {
	var parsed struct {
		IsConnected bool `json:"isConnected"`
	}
	err := c.call(ctx, http.MethodGet, "/client/connection-test", nil, &parsed)
	return parsed.IsConnected, err
}

// Settings answers where Flood's client saves torrents.
func (c Client) Settings(ctx context.Context) (string, error) {
	var parsed struct {
		Directory string `json:"directoryDefault"`
	}
	err := c.call(ctx, http.MethodGet, "/client/settings", nil, &parsed)
	return parsed.Directory, err
}

type addFiles struct {
	Files []string `json:"files"`
	Tags  []string `json:"tags,omitempty"`
	Start bool     `json:"start"`
}

type addURLs struct {
	URLs  []string `json:"urls"`
	Tags  []string `json:"tags,omitempty"`
	Start bool     `json:"start"`
}

func tags(category string) []string {
	if category == "" {
		return nil
	}
	return []string{category}
}

// AddFile sends a base64 torrent file, tagged with category.
func (c Client) AddFile(ctx context.Context, base64File, category string) error {
	return c.call(ctx, http.MethodPost, "/torrents/add-files", addFiles{Files: []string{base64File}, Tags: tags(category), Start: true}, nil)
}

// AddURL sends a magnet link, tagged with category.
func (c Client) AddURL(ctx context.Context, magnet, category string) error {
	return c.call(ctx, http.MethodPost, "/torrents/add-urls", addURLs{URLs: []string{magnet}, Tags: tags(category), Start: true}, nil)
}

// Torrent is the part of Flood's torrent list one download needs.
type Torrent struct {
	Hash      string   `json:"hash"`
	Name      string   `json:"name"`
	Status    []string `json:"status"`
	SizeBytes int64    `json:"sizeBytes"`
	BytesDone int64    `json:"bytesDone"`
	Directory string   `json:"directory"`
	Ratio     float64  `json:"ratio"`
	Tags      []string `json:"tags"`
	DownRate  int64    `json:"downRate"`
	Message   string   `json:"message"`
}

// Torrents answers every torrent.
func (c Client) Torrents(ctx context.Context) ([]Torrent, error) {
	var parsed struct {
		Torrents map[string]Torrent `json:"torrents"`
	}
	if err := c.call(ctx, http.MethodGet, "/torrents", nil, &parsed); err != nil {
		return nil, err
	}
	torrents := make([]Torrent, 0, len(parsed.Torrents))
	for hash, torrent := range parsed.Torrents {
		torrent.Hash = hash
		torrents = append(torrents, torrent)
	}
	return torrents, nil
}

// Delete removes torrents by hash; deleteData also deletes what they downloaded.
func (c Client) Delete(ctx context.Context, hashes []string, deleteData bool) error {
	return c.call(ctx, http.MethodPost, "/torrents/delete", struct {
		Hashes     []string `json:"hashes"`
		DeleteData bool     `json:"deleteData"`
	}{hashes, deleteData}, nil)
}

// SetStarted stops or starts torrents by hash.
func (c Client) SetStarted(ctx context.Context, hashes []string, started bool) error {
	path := "/torrents/stop"
	if started {
		path = "/torrents/start"
	}
	return c.call(ctx, http.MethodPost, path, struct {
		Hashes []string `json:"hashes"`
	}{hashes}, nil)
}
