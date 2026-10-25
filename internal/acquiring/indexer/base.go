package indexer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

// Base is what every API indexer shares (Prowlarr's TorrentIndexerBase): bounded requests, fetching the file a result
// links to, the capabilities it declares, and the text of a request. An implementation embeds it and adds Search.
type Base struct {
	HTTP    *httpx.Client
	BaseURL string
	// Caps are what the implementation searches; it never reads them from the site.
	Caps Capabilities
	// Header is added to every request: the API key of an indexer that takes it as a header.
	Header http.Header
}

// NewBase builds one; baseURL loses its trailing slash.
func NewBase(client *httpx.Client, baseURL string, caps Capabilities) Base {
	return Base{HTTP: client, BaseURL: strings.TrimRight(baseURL, "/"), Caps: caps}
}

// Do performs one request with the indexer's headers and answers its bounded body.
func (b Base) Do(ctx context.Context, method, target string, body io.Reader, contentType string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, err
	}
	for key, values := range b.Header {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := b.HTTP.Do(ctx, request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	return httpx.ReadLimited(response.Body, maxResponseBytes)
}

// GetJSON fetches target and decodes it into into.
func (b Base) GetJSON(ctx context.Context, target string, into any) error {
	body, err := b.Do(ctx, http.MethodGet, target, nil, "")
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("indexer: decode response: %w", err)
	}
	return nil
}

// PostJSON sends payload as JSON to target and decodes the answer into into.
func (b Base) PostJSON(ctx context.Context, target string, payload, into any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	body, err := b.Do(ctx, http.MethodPost, target, strings.NewReader(string(encoded)), "application/json")
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("indexer: decode response: %w", err)
	}
	return nil
}

// Capabilities answers what the implementation declared.
func (b Base) Capabilities(context.Context) (Capabilities, error) { return b.Caps, nil }

// Fetch downloads a result's .torrent with the indexer's headers; a magnet link is the release itself.
func (b Base) Fetch(ctx context.Context, link string) ([]byte, error) {
	if strings.HasPrefix(link, "magnet:") {
		return []byte(link), nil
	}
	body, err := b.Do(ctx, http.MethodGet, link, nil, "")
	var magnet *httpx.MagnetRedirect
	if errors.As(err, &magnet) {
		return []byte(magnet.Link), nil
	}
	if err != nil {
		return nil, fmt.Errorf("indexer: fetch release: %w", err)
	}
	if len(body) == 0 {
		return nil, errors.New("indexer: fetch release: empty response")
	}
	return body, nil
}

// Text is a request's words: its query, or what its structured parameters spell (artist and album, a title with its
// season and episode) for an indexer that only takes words.
func Text(request SearchRequest) string {
	if request.Query != "" {
		return request.Query
	}
	var words []string
	for _, key := range []string{"artist", "album", "q", "year"} {
		if value := request.Extra[key]; value != "" {
			words = append(words, value)
		}
	}
	season, _ := strconv.Atoi(request.Extra["season"])
	episode, _ := strconv.Atoi(request.Extra["ep"])
	switch {
	case season > 0 && episode > 0:
		words = append(words, fmt.Sprintf("S%02dE%02d", season, episode))
	case season > 0:
		words = append(words, fmt.Sprintf("S%02d", season))
	}
	return strings.Join(words, " ")
}

// Wants reports whether a result in categories answers a request for requested: any of them in the same top
// category, or a request for none. A result without categories answers anything.
func Wants(requested, categories []int) bool {
	if len(requested) == 0 || len(categories) == 0 {
		return true
	}
	for _, want := range requested {
		for _, have := range categories {
			if want/1000 == have/1000 {
				return true
			}
		}
	}
	return false
}

// Keep drops the results a request's categories do not want, and bounds them by its limit.
func Keep(request SearchRequest, items []RawItem) []RawItem {
	kept := items[:0]
	for _, item := range items {
		if Wants(request.Categories, item.Categories) {
			kept = append(kept, item)
		}
	}
	if request.Limit > 0 && len(kept) > request.Limit {
		kept = kept[:request.Limit]
	}
	return kept
}

// StaticCaps declares an implementation's capabilities: the search functions it answers (each with the parameters
// it takes) and its top categories.
func StaticCaps(functions map[string][]string, categories ...int) Capabilities {
	caps := Capabilities{Search: map[string]SearchFunction{}}
	for _, name := range []string{"search", "tv-search", "movie-search", "music-search", "book-search", "audio-search"} {
		params, available := functions[name]
		caps.Search[name] = SearchFunction{Available: available, SupportedParams: params}
	}
	for _, id := range categories {
		caps.Categories = append(caps.Categories, Category{ID: id, Name: categoryNames[id]})
	}
	return caps
}

var categoryNames = map[int]string{
	2000: "Movies", 3000: "Audio", 5000: "TV", 5070: "TV/Anime", 7000: "Books",
}

// Magnet builds a magnet link for an info hash, with trackers a public torrent is commonly announced on.
func Magnet(hash, name string, trackers []string) string {
	query := url.Values{"xt": {"urn:btih:" + strings.ToLower(hash)}, "dn": {name}}
	sorted := append([]string(nil), trackers...)
	sort.Strings(sorted)
	link := "magnet:?" + query.Encode()
	for _, tracker := range sorted {
		link += "&tr=" + url.QueryEscape(tracker)
	}
	return link
}

// FlexInt is a number an API sends as a number or as a string.
type FlexInt int64

// UnmarshalJSON reads 12, "12" and "" (zero).
func (f *FlexInt) UnmarshalJSON(data []byte) error {
	text := strings.Trim(string(data), `"`)
	if text == "" || text == "null" {
		*f = 0
		return nil
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return fmt.Errorf("indexer: %s is not a number", data)
	}
	*f = FlexInt(value)
	return nil
}
