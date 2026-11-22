package indexer

import (
	"context"
	"encoding/xml"
	"fmt"
	"html"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// SearchRequest is one t=<Function> query: "search", "tvsearch", "movie", "music", "book".
// Caps name them differently (music-search); the t= value is not the caps element name.
type SearchRequest struct {
	Function   string
	Query      string
	Categories []int
	Limit      int
	// Offset skips that many results, to read the page after a full one; the native clients ignore it.
	Offset int
	Extra  map[string]string
}

// Search fetches and parses one t=<function> response into raw items; title parsing, dedupe and scoring are ADR-18/ADR-40's jobs.
func (c Client) Search(ctx context.Context, request SearchRequest) ([]RawItem, error) {
	body, err := c.getBody(ctx, c.apiURL(request.function(), request.params()))
	if err != nil {
		return nil, fmt.Errorf("indexer: search: %w", err)
	}
	return parseItems(body)
}

// Validators are the HTTP cache validators an indexer answered a feed with; zero asks unconditionally.
type Validators struct {
	ETag         string
	LastModified string
}

// SearchIfChanged is Search with the validators of the last answer; unchanged reports a 304, which carries no items.
func (c Client) SearchIfChanged(ctx context.Context, request SearchRequest, seen Validators) (items []RawItem, now Validators, unchanged bool, err error) {
	response, err := c.getConditional(ctx, c.apiURL(request.function(), request.params()), seen)
	if err != nil {
		return nil, seen, false, fmt.Errorf("indexer: search: %w", err)
	}
	if response.unchanged {
		return nil, seen, true, nil
	}
	items, err = parseItems(response.body)
	return items, response.validators, false, err
}

func (r SearchRequest) function() string {
	if r.Function == "" {
		return "search"
	}
	return r.Function
}

func (r SearchRequest) params() url.Values {
	params := url.Values{}
	if r.Query != "" {
		params.Set("q", r.Query)
	}
	if len(r.Categories) > 0 {
		categories := make([]string, len(r.Categories))
		for i, cat := range r.Categories {
			categories[i] = strconv.Itoa(cat)
		}
		params.Set("cat", strings.Join(categories, ","))
	}
	if r.Limit > 0 {
		params.Set("limit", strconv.Itoa(r.Limit))
		if r.Offset > 0 {
			params.Set("offset", strconv.Itoa(r.Offset))
		}
	}
	for key, value := range r.Extra {
		params.Set(key, value)
	}
	return params
}

func parseItems(body []byte) ([]RawItem, error) {
	var parsed rssXML
	if err := xml.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("indexer: parse search response: %w", err)
	}
	items := make([]RawItem, len(parsed.Channel.Item))
	for i, item := range parsed.Channel.Item {
		items[i] = item.toRawItem()
	}
	return items, nil
}

// rssXML is one t=search/t=<function> response -- RSS 2.0 with the Newznab
// attribute extension, shared by Torznab (ADR-43).
type rssXML struct {
	XMLName xml.Name      `xml:"rss"`
	Channel rssChannelXML `xml:"channel"`
}

type rssChannelXML struct {
	Item []rssItemXML `xml:"item"`
}

type rssItemXML struct {
	Title     string          `xml:"title"`
	GUID      string          `xml:"guid"`
	Link      string          `xml:"link"`
	PubDate   string          `xml:"pubDate"`
	Enclosure rssEnclosureXML `xml:"enclosure"`
	Attr      []rssAttrXML    `xml:"attr"`
}

type rssEnclosureXML struct {
	URL    string `xml:"url,attr"`
	Length string `xml:"length,attr"`
}

type rssAttrXML struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

// attr returns every value recorded under name -- category is the one
// newznab:attr that legitimately repeats (a release can carry more than one
// category id).
func (item rssItemXML) attrs(name string) []string {
	var values []string
	for _, attr := range item.Attr {
		if attr.Name == name {
			values = append(values, attr.Value)
		}
	}
	return values
}

func (item rssItemXML) attr(name string) string {
	values := item.attrs(name)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func (item rssItemXML) toRawItem() RawItem {
	link := item.Enclosure.URL
	if link == "" {
		link = item.Link
	}

	sizeBytes, _ := strconv.ParseInt(item.Enclosure.Length, 10, 64)
	if sizeBytes == 0 {
		sizeBytes, _ = strconv.ParseInt(item.attr("size"), 10, 64)
	}

	var categories []int
	for _, raw := range item.attrs("category") {
		if id, err := strconv.Atoi(raw); err == nil {
			categories = append(categories, id)
		}
	}

	seeders, _ := strconv.Atoi(item.attr("seeders"))
	peers, _ := strconv.Atoi(item.attr("peers"))

	grabs, _ := strconv.Atoi(item.attr("grabs"))
	minimumRatio, _ := strconv.ParseFloat(item.attr("minimumratio"), 64)
	seedSeconds, _ := strconv.ParseFloat(item.attr("minimumseedtime"), 64)
	var volumeFactor *float64
	if factor, err := strconv.ParseFloat(item.attr("downloadvolumefactor"), 64); err == nil {
		volumeFactor = &factor
	}

	return RawItem{
		Title: html.UnescapeString(item.Title), GUID: item.GUID, Link: link,
		Categories: categories, SizeBytes: sizeBytes,
		PublishedAt: parsePubDate(item.PubDate),
		Seeders:     seeders, Peers: peers, InfoHash: item.attr("infohash"),
		IDs: item.providerIDs(), Season: item.attr("season"), Episode: item.attr("episode"),
		Passworded: item.attr("password") == "1", PostedAt: parsePubDate(item.attr("usenetdate")),
		Grabs: grabs, MagnetURL: item.attr("magneturl"),
		// Torznab states the seed time in seconds.
		MinimumSeedTime: time.Duration(seedSeconds * float64(time.Second)), MinimumRatio: minimumRatio,
		DownloadVolumeFactor: volumeFactor,
	}
}

// providerIDs reads the ids the indexer attached; "0" and empty mean none.
func (item rssItemXML) providerIDs() map[string]string {
	var ids map[string]string
	for provider, names := range map[string][]string{
		"tvdb": {"tvdbid", "tvdb"}, "tvmaze": {"tvmazeid", "tvmaze"}, "tmdb": {"tmdbid", "tmdb"}, "imdb": {"imdbid", "imdb"},
	} {
		for _, name := range names {
			value := strings.TrimSpace(item.attr(name))
			if value == "" || strings.Trim(value, "0") == "" {
				continue
			}
			if provider == "imdb" && !strings.HasPrefix(value, "tt") {
				value = "tt" + value
			}
			if ids == nil {
				ids = map[string]string{}
			}
			ids[provider] = value
			break
		}
	}
	return ids
}

// pubDate layouts seen in the wild, tried in order -- RFC1123Z (numeric
// offset) is the common case, then a named zone, a one-digit day, no weekday, RFC 822, ISO 8601.
var pubDateLayouts = []string{
	time.RFC1123Z, time.RFC1123,
	"Mon, 2 Jan 2006 15:04:05 -0700", "Mon, 2 Jan 2006 15:04:05 MST",
	"2 Jan 2006 15:04:05 -0700", "02 Jan 2006 15:04:05 -0700", "2 Jan 2006 15:04:05 MST",
	time.RFC822Z, time.RFC822, time.RFC3339, time.DateTime,
}

// parsePubDate returns the zero time for an unparseable date, since a malformed pubDate must not fail the search and the pipeline tolerates a zero PublishedAt.
func parsePubDate(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	for _, layout := range pubDateLayouts {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

// IDs is the ids of a native tracker's answer, in RawItem.IDs' form; "0" and empty mean none and an IMDb id gets its "tt".
func IDs(tvdb, imdb string) map[string]string {
	var ids map[string]string
	add := func(provider, value string) {
		value = strings.TrimSpace(value)
		if value == "" || strings.Trim(value, "0") == "" {
			return
		}
		if provider == "imdb" && !strings.HasPrefix(value, "tt") {
			value = "tt" + value
		}
		if ids == nil {
			ids = map[string]string{}
		}
		ids[provider] = value
	}
	add("tvdb", tvdb)
	add("imdb", imdb)
	return ids
}
