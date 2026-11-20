package indexer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
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

// Numbering is the season and episode a request names. A daily episode carries its air date as "MM/DD" with the year as
// the season (Newznab's convention); a season of 0 is the specials and is a real season.
type Numbering struct {
	Season, Episode int
	// HasSeason says the request names a season at all, 0 included.
	HasSeason bool
	// Daily is the "MM/DD" of a daily episode, empty otherwise.
	Daily string
}

// NumberingOf reads a request's season and episode; an episode that does not parse counts as none.
func NumberingOf(request SearchRequest) Numbering {
	var numbering Numbering
	season, err := strconv.Atoi(request.Extra["season"])
	numbering.Season, numbering.HasSeason = season, err == nil && season >= 0
	if day := request.Extra["ep"]; strings.Contains(day, "/") {
		if numbering.HasSeason {
			numbering.Daily = day
		}
		return numbering
	}
	if episode, err := strconv.Atoi(request.Extra["ep"]); err == nil && episode > 0 {
		numbering.Episode = episode
	}
	return numbering
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
	numbering := NumberingOf(request)
	switch {
	case numbering.Daily != "":
		words = append(words, fmt.Sprintf("%d %s", numbering.Season, strings.ReplaceAll(numbering.Daily, "/", " ")))
	case numbering.HasSeason && numbering.Episode > 0:
		words = append(words, fmt.Sprintf("S%02dE%02d", numbering.Season, numbering.Episode))
	case numbering.HasSeason:
		words = append(words, fmt.Sprintf("S%02d", numbering.Season))
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

// answerCode is the code of a Newznab error answer that begins an answer, 0 when the answer is none.
func answerCode(start []byte) int {
	text := string(start)
	i := strings.Index(text, "<error")
	if i < 0 {
		return 0
	}
	head := text[i:]
	if end := strings.Index(head, ">"); end >= 0 {
		head = head[:end]
	}
	_, after, found := strings.Cut(head, `code="`)
	if !found {
		return 0
	}
	digits, _, _ := strings.Cut(after, `"`)
	code, err := strconv.Atoi(digits)
	if err != nil {
		return 0
	}
	return code
}

// LimitReached reports the start of an answer that is a Newznab "request limit" or "download limit" error (codes 500
// and 501), which an indexer sends with a 200 status: the caller must back off, not take it for a search with no results.
func LimitReached(start []byte) bool {
	code := answerCode(start)
	return code == 500 || code == 501 || jsonLimitAnswer.Match(start[:min(len(start), jsonLimitWindow)])
}

// jsonLimitWindow is how much of an answer's start is read for a JSON tracker's limit answer, which comes first and is short.
const jsonLimitWindow = 300

// jsonLimitAnswer matches the JSON trackers' limit answers, anchored to the start so a release named alike is not one:
// BroadcasTheNet's JSON-RPC "Call Limit Exceeded" error, and a Gazelle site's failure that speaks of a rate limit.
var jsonLimitAnswer = regexp.MustCompile(
	`(?is)^\s*\{(?:[^{}]*"error"\s*:\s*\{[^{}]*call limit exceeded|\s*"status"\s*:\s*"failure"[^{}]*(?:rate.?limit|too many requests))`)

// AccountRefused reports an answer that says the key is wrong or suspended or the API is off (codes 100 to 102 and
// 910): the indexer is unhealthy until someone fixes it, which the breaker must see rather than an empty result.
func AccountRefused(start []byte) bool {
	code := answerCode(start)
	return (code >= 100 && code <= 102) || code == 910
}
