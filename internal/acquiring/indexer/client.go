package indexer

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/makearr/MediaNexus/internal/platform/httpx"
)

// maxResponseBytes bounds what one caps or search call can make this
// process read -- the same defensive ceiling internal/importing/arrimport
// and internal/metadata/musicbrainz both already apply to a provider they
// do not control. A real indexer's own search page is nowhere near this;
// one answering with more is refused rather than read without limit.
const maxResponseBytes = 8 << 20

// Client is a thin Newznab/Torznab protocol client over one indexer
// connection. It has no opinion on rate limiting, circuit breaking or
// egress proxying (ADR-13/47) -- client must already be configured for
// that, the same split LidarrClient already uses.
type Client struct {
	http    *httpx.Client
	baseURL string
	apiKey  string
}

// NewClient builds one over an already-configured httpx.Client.
func NewClient(client *httpx.Client, baseURL, apiKey string) Client {
	return Client{http: client, baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey}
}

// apiURL builds one t=<function> request URL, adding the API key only when
// this connection has one -- ADR-43/47: a public Torznab endpoint may need
// none, and the caller must not be forced to invent one.
func (c Client) apiURL(function string, params url.Values) string {
	values := url.Values{}
	for key, vals := range params {
		values[key] = vals
	}
	values.Set("t", function)
	if c.apiKey != "" {
		values.Set("apikey", c.apiKey)
	}
	return c.baseURL + "/api?" + values.Encode()
}

// getBody performs the request and returns its bounded, read-to-completion
// body -- the shared shape every Newznab/Torznab call needs before its own
// XML parse.
func (c Client) getBody(ctx context.Context, requestURL string) ([]byte, error) {
	response, err := c.http.Get(ctx, requestURL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("indexer: read response: %w", err)
	}
	return body, nil
}
