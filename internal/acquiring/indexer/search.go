package indexer

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// SearchRequest is one query's own parameters. Function names one of
// Newznab's own search modes ("search", "tv-search", "movie-search",
// "music-search", "book-search", "audio-search") -- this package has no
// opinion on which one a caller should use for a given media type, only how
// to send whichever is chosen. Extra carries function-specific parameters
// (music-search's own artist/album/label/track/year/genre, for instance) --
// kept generic here rather than hardcoded, since indexer knows nothing
// about music specifically (ADR-23).
type SearchRequest struct {
	Function   string
	Query      string
	Categories []int
	Limit      int
	Extra      map[string]string
}

// Search fetches and parses one t=<function> response into raw items --
// this package does no title parsing, deduping or scoring of its own
// (ADR-18/ADR-40's own jobs); it only turns the indexer's own protocol
// response into a plain Go value.
func (c Client) Search(ctx context.Context, request SearchRequest) ([]RawItem, error) {
	function := request.Function
	if function == "" {
		function = "search"
	}

	params := url.Values{}
	if request.Query != "" {
		params.Set("q", request.Query)
	}
	if len(request.Categories) > 0 {
		categories := make([]string, len(request.Categories))
		for i, cat := range request.Categories {
			categories[i] = strconv.Itoa(cat)
		}
		params.Set("cat", strings.Join(categories, ","))
	}
	if request.Limit > 0 {
		params.Set("limit", strconv.Itoa(request.Limit))
	}
	for key, value := range request.Extra {
		params.Set(key, value)
	}

	requestURL := c.apiURL(function, params)
	body, err := c.getBody(ctx, requestURL)
	if err != nil {
		return nil, fmt.Errorf("indexer: search: %w", err)
	}

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

	return RawItem{
		Title: item.Title, GUID: item.GUID, Link: link,
		Categories: categories, SizeBytes: sizeBytes,
		PublishedAt: parsePubDate(item.PubDate),
		Seeders:     seeders, Peers: peers, InfoHash: item.attr("infohash"),
	}
}

// pubDate layouts seen in the wild, tried in order -- RFC1123Z (numeric
// offset) is the common case, RFC1123 (named zone) the fallback.
var pubDateLayouts = []string{time.RFC1123Z, time.RFC1123}

// parsePubDate returns the zero time for a date it cannot parse -- a
// malformed pubDate is not a reason to fail the whole search, and the
// candidate pipeline's own dedupe/expiry logic already tolerates a zero
// PublishedAt the same way it tolerates any other missing fact.
func parsePubDate(raw string) time.Time {
	for _, layout := range pubDateLayouts {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed
		}
	}
	return time.Time{}
}
