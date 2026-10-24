// Package sabnzbd is stage 5's own SABnzbd downloader client (ADR-46/57):
// query-string API, JSON responses. It has no opinion on rate limiting,
// circuit breaking or egress proxying -- the caller supplies an already-
// configured httpx.Client, the same split internal/acquiring/indexer and
// internal/importing/arrimport's own LidarrClient already use for a
// per-connection external service. Downloader puts it behind download.Client (ADR-46).
package sabnzbd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/platform/httpx"
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

	body, err := httpx.ReadLimited(response.Body, maxResponseBytes)
	if err != nil {
		return fmt.Errorf("sabnzbd: read response: %w", err)
	}
	// SABnzbd answers a refused key in plain text, not JSON.
	if bytes.HasPrefix(bytes.TrimSpace(body), []byte("API Key")) {
		return ErrAPIKeyRefused
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("sabnzbd: decode response: %w", err)
	}
	return nil
}

// ErrAPIKeyRefused is SABnzbd refusing the API key.
var ErrAPIKeyRefused = errors.New("sabnzbd: the API key was refused")

// Version answers the instance's own version. SABnzbd answers it without a key, so it proves the
// address, never the key; CompleteFolder does that.
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
	bytes := value * 1024 * 1024
	if err != nil || math.IsNaN(bytes) || math.IsInf(bytes, 0) || bytes < 0 || bytes >= float64(1<<63) {
		return 0
	}
	return int64(bytes)
}

// kbPerSecToBytesPerSec converts one of SABnzbd's own "4800.00"-shaped
// kilobyte-per-second strings to bytes/sec.
func kbPerSecToBytesPerSec(kb string) int64 {
	value, err := strconv.ParseFloat(kb, 64)
	bytes := value * 1024
	if err != nil || math.IsNaN(bytes) || math.IsInf(bytes, 0) || bytes < 0 || bytes >= float64(1<<63) {
		return 0
	}
	return int64(bytes)
}

// CompleteFolder answers where SABnzbd puts finished downloads. It needs the full API key, so a
// connection test that reads it has proved the key too.
func (c Client) CompleteFolder(ctx context.Context) (string, error) {
	var parsed struct {
		Config struct {
			Misc struct {
				CompleteDir string `json:"complete_dir"`
			} `json:"misc"`
		} `json:"config"`
	}
	params := url.Values{"section": {"misc"}, "keyword": {"complete_dir"}}
	if err := c.getJSON(ctx, c.apiURL("get_config", params), &parsed); err != nil {
		return "", fmt.Errorf("sabnzbd: complete folder: %w", err)
	}
	return parsed.Config.Misc.CompleteDir, nil
}

// Categories answers the categories SABnzbd knows, "*" included.
func (c Client) Categories(ctx context.Context) ([]string, error) {
	var parsed struct {
		Categories []string `json:"categories"`
	}
	if err := c.getJSON(ctx, c.apiURL("get_cats", nil), &parsed); err != nil {
		return nil, fmt.Errorf("sabnzbd: categories: %w", err)
	}
	return parsed.Categories, nil
}
