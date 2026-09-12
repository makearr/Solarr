// Package sabnzbd is stage 5's own SABnzbd downloader client (ADR-46/57):
// query-string API, JSON responses. It has no opinion on rate limiting,
// circuit breaking or egress proxying -- the caller supplies an already-
// configured httpx.Client, the same split internal/acquiring/indexer and
// internal/importing/arrimport's own LidarrClient already use for a
// per-connection external service. Only one implementation exists today, so
// there is no Downloader interface here yet (ADR-46 names qBittorrent as a
// later stage's own work) -- generalising before a second implementation
// exists would just be a guess at its shape.
package sabnzbd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"

	"github.com/makearr/MediaNexus/internal/platform/httpx"
)

// maxResponseBytes bounds what one call can make this process read -- the
// same defensive ceiling internal/acquiring/indexer and internal/importing/
// arrimport both already apply to a provider they do not control.
const maxResponseBytes = 8 << 20

// Client is a thin SABnzbd API client over one downloader connection.
type Client struct {
	http    *httpx.Client
	baseURL string
	apiKey  string
}

// NewClient builds one over an already-configured httpx.Client.
func NewClient(client *httpx.Client, baseURL, apiKey string) Client {
	return Client{http: client, baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey}
}

// apiURL builds one mode=<mode> request URL.
func (c Client) apiURL(mode string, params url.Values) string {
	values := url.Values{}
	for key, vals := range params {
		values[key] = vals
	}
	values.Set("mode", mode)
	values.Set("output", "json")
	values.Set("apikey", c.apiKey)
	return c.baseURL + "/api?" + values.Encode()
}

// getJSON performs the request and decodes its bounded body -- the shared
// shape every SABnzbd call needs, the same pattern internal/metadata/
// musicbrainz's own getJSON uses.
func (c Client) getJSON(ctx context.Context, requestURL string, into any) error {
	response, err := c.http.Get(ctx, requestURL)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("sabnzbd: read response: %w", err)
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("sabnzbd: decode response: %w", err)
	}
	return nil
}

// Version answers the instance's own version -- a connection test: a wrong
// base URL or API key fails here, before anything else asks it. The same
// role LidarrClient's own SystemStatus plays for a *Arr connection.
func (c Client) Version(ctx context.Context) (string, error) {
	var parsed struct {
		Version string `json:"version"`
	}
	requestURL := c.apiURL("version", nil)
	if err := c.getJSON(ctx, requestURL, &parsed); err != nil {
		return "", fmt.Errorf("sabnzbd: version: %w", err)
	}
	return parsed.Version, nil
}

// megabytesToBytes converts one of SABnzbd's own "12583.5"-shaped megabyte
// strings to bytes -- every size field in the queue/history API is a decimal
// string in MB, not a raw byte count. An unparseable value answers 0 rather
// than failing the whole response over one field SABnzbd itself might one
// day format differently.
func megabytesToBytes(mb string) int64 {
	value, err := strconv.ParseFloat(mb, 64)
	if err != nil {
		return 0
	}
	return int64(value * 1024 * 1024)
}

// kbPerSecToBytesPerSec converts one of SABnzbd's own "4800.00"-shaped
// kilobyte-per-second strings to bytes/sec.
func kbPerSecToBytesPerSec(kb string) int64 {
	value, err := strconv.ParseFloat(kb, 64)
	if err != nil {
		return 0
	}
	return int64(value * 1024)
}
