// Package nyaa is the Nyaa indexer of anime and East Asian media, read through the site's own RSS search, which
// carries each torrent's seeders, size and info hash.
package nyaa

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// Definition is Nyaa as the catalog lists it.
func Definition() indexer.Definition {
	return indexer.Definition{
		ID: "nyaasi", Name: "Nyaa.si", Impl: indexer.ImplNyaa, Privacy: indexer.PrivacyPublic, Language: "en-US",
		BaseURLs:    []string{"https://nyaa.si", "https://nyaa.land"},
		MediaTypes:  []string{indexer.MediaAnime, indexer.MediaMusic, indexer.MediaBooks},
		Description: "Nyaa is a public tracker of anime, its music and manga, searched through its RSS feed.",
	}
}

// Indexer searches Nyaa.
type Indexer struct{ indexer.Base }

var _ indexer.Driver = Indexer{}

// New builds one at baseURL.
func New(client *httpx.Client, baseURL string) Indexer {
	caps := indexer.StaticCaps(map[string][]string{
		"search": {"q"}, "tv-search": {"q", "season", "ep"}, "music-search": {"q"}, "book-search": {"q"}, "movie-search": {"q"},
	}, 2000, 3000, 5070, 7000)
	return Indexer{Base: indexer.NewBase(client, baseURL, caps)}
}

type feed struct {
	Items []item `xml:"channel>item"`
}

type item struct {
	Title      string `xml:"title"`
	Link       string `xml:"link"`
	GUID       string `xml:"guid"`
	PubDate    string `xml:"pubDate"`
	Seeders    int    `xml:"https://nyaa.si/xmlns/nyaa seeders"`
	Leechers   int    `xml:"https://nyaa.si/xmlns/nyaa leechers"`
	InfoHash   string `xml:"https://nyaa.si/xmlns/nyaa infoHash"`
	CategoryID string `xml:"https://nyaa.si/xmlns/nyaa categoryId"`
	Size       string `xml:"https://nyaa.si/xmlns/nyaa size"`
}

// Search reads Nyaa's RSS search; no words reads its newest.
func (i Indexer) Search(ctx context.Context, request indexer.SearchRequest) ([]indexer.RawItem, error) {
	query := url.Values{"page": {"rss"}, "c": {"0_0"}, "f": {"0"}}
	if text := indexer.Text(request); text != "" {
		query.Set("q", text)
	}
	body, err := i.Do(ctx, http.MethodGet, i.BaseURL+"/?"+query.Encode(), nil, "")
	if err != nil {
		return nil, err
	}
	var parsed feed
	if err := xml.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("nyaa: parse feed: %w", err)
	}
	items := make([]indexer.RawItem, 0, len(parsed.Items))
	for _, found := range parsed.Items {
		published, _ := time.Parse(time.RFC1123Z, found.PubDate)
		items = append(items, indexer.RawItem{
			Title: found.Title, GUID: found.GUID, Link: found.Link, Categories: []int{category(found.CategoryID)},
			SizeBytes: Size(found.Size), PublishedAt: published, Seeders: found.Seeders,
			Peers: found.Seeders + found.Leechers, InfoHash: strings.ToLower(found.InfoHash),
		})
	}
	return indexer.Keep(request, items), nil
}

// categories maps Nyaa's "main_sub" categories onto Newznab's.
var categories = map[string]int{
	"1_1": 5070, "1_2": 5070, "1_3": 5070, "1_4": 5070, "2_1": 3040, "2_2": 3010, "3_1": 7000, "3_2": 7000, "3_3": 7000,
	"4_1": 2000, "4_2": 2000, "4_3": 2000, "4_4": 2000,
}

func category(id string) int {
	if mapped, found := categories[id]; found {
		return mapped
	}
	switch strings.SplitN(id, "_", 2)[0] {
	case "1":
		return 5070
	case "2":
		return 3000
	case "3":
		return 7000
	}
	return 8000
}

// Size reads Nyaa's "1.2 GiB"-shaped sizes; anything unreadable is 0.
func Size(text string) int64 {
	number, unit, _ := strings.Cut(strings.TrimSpace(text), " ")
	value, err := strconv.ParseFloat(number, 64)
	if err != nil {
		return 0
	}
	multiplier := map[string]float64{"B": 1, "KiB": 1 << 10, "MiB": 1 << 20, "GiB": 1 << 30, "TiB": 1 << 40}[unit]
	return int64(value * multiplier)
}
