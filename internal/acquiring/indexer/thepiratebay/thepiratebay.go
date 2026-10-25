// Package thepiratebay is Prowlarr's native The Pirate Bay indexer: apibay's JSON search, every result a magnet link
// with the trackers the site announces on.
package thepiratebay

import (
	"context"
	"net/url"
	"strconv"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// Definition is The Pirate Bay as the catalog lists it.
func Definition() indexer.Definition {
	return indexer.Definition{
		ID: "thepiratebay", Name: "The Pirate Bay", Impl: indexer.ImplThePirateBay, Privacy: indexer.PrivacyPublic,
		Language: "en-US", BaseURLs: []string{"https://apibay.org"},
		MediaTypes:  []string{indexer.MediaMusic, indexer.MediaTV, indexer.MediaMovies, indexer.MediaBooks},
		Description: "The Pirate Bay through its apibay JSON API: the largest public tracker, no account.",
	}
}

// trackers are what apibay's own site adds to its magnet links.
var trackers = []string{
	"udp://tracker.opentrackr.org:1337/announce", "udp://open.stealth.si:80/announce",
	"udp://tracker.torrent.eu.org:451/announce", "udp://tracker.openbittorrent.com:6969/announce",
	"udp://exodus.desync.com:6969/announce", "udp://tracker.tiny-vps.com:6969/announce",
}

// Indexer searches apibay.
type Indexer struct{ indexer.Base }

var _ indexer.Driver = Indexer{}

// New builds one at baseURL (apibay.org unless a mirror is set).
func New(client *httpx.Client, baseURL string) Indexer {
	caps := indexer.StaticCaps(map[string][]string{
		"search": {"q"}, "tv-search": {"q", "season", "ep"}, "movie-search": {"q"}, "music-search": {"q"}, "book-search": {"q"},
	}, 2000, 3000, 5000, 7000)
	return Indexer{Base: indexer.NewBase(client, baseURL, caps)}
}

type result struct {
	ID       indexer.FlexInt `json:"id"`
	Name     string          `json:"name"`
	InfoHash string          `json:"info_hash"`
	Seeders  indexer.FlexInt `json:"seeders"`
	Leechers indexer.FlexInt `json:"leechers"`
	Size     indexer.FlexInt `json:"size"`
	Added    indexer.FlexInt `json:"added"`
	Category indexer.FlexInt `json:"category"`
}

// noResults is the hash apibay answers an empty search with, as a single placeholder row.
const noResults = "0000000000000000000000000000000000000000"

// Search asks apibay; no words reads its hundred newest.
func (i Indexer) Search(ctx context.Context, request indexer.SearchRequest) ([]indexer.RawItem, error) {
	target := i.BaseURL + "/precompiled/data_top100_recent.json"
	if text := indexer.Text(request); text != "" {
		target = i.BaseURL + "/q.php?" + url.Values{"q": {text}}.Encode()
	}
	var results []result
	if err := i.GetJSON(ctx, target, &results); err != nil {
		return nil, err
	}
	items := make([]indexer.RawItem, 0, len(results))
	for _, found := range results {
		if found.InfoHash == noResults || found.InfoHash == "" {
			continue
		}
		items = append(items, indexer.RawItem{
			Title: found.Name, GUID: "https://thepiratebay.org/description.php?id=" + strconv.FormatInt(int64(found.ID), 10),
			Link: indexer.Magnet(found.InfoHash, found.Name, trackers), Categories: []int{category(int(found.Category))},
			SizeBytes: int64(found.Size), PublishedAt: time.Unix(int64(found.Added), 0).UTC(),
			Seeders: int(found.Seeders), Peers: int(found.Seeders + found.Leechers), InfoHash: found.InfoHash,
		})
	}
	return indexer.Keep(request, items), nil
}

// categories maps The Pirate Bay's own categories onto Newznab's; others fall back by their hundred.
var categories = map[int]int{
	102: 3030, 104: 3040, 201: 2000, 202: 2070, 205: 5000, 207: 2040, 208: 5040, 209: 2060, 211: 2045, 212: 5045, 601: 7020,
}

func category(code int) int {
	if mapped, found := categories[code]; found {
		return mapped
	}
	switch code / 100 {
	case 1:
		return 3000
	case 2:
		return 2000
	case 6:
		return 7000
	}
	return 8000
}
