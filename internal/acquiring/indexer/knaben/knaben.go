// Package knaben is Prowlarr's native Knaben indexer: a public meta-search over many trackers, through its JSON API.
package knaben

import (
	"context"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// Definition is Knaben as the catalog lists it.
func Definition() indexer.Definition {
	return indexer.Definition{
		ID: "knaben", Name: "Knaben", Impl: indexer.ImplKnaben, Privacy: indexer.PrivacyPublic, Language: "en-US",
		BaseURLs:    []string{"https://api.knaben.org"},
		MediaTypes:  []string{indexer.MediaMusic, indexer.MediaTV, indexer.MediaMovies, indexer.MediaAnime, indexer.MediaBooks},
		Description: "Knaben searches many public trackers at once (The Pirate Bay, 1337x, Nyaa and more).",
	}
}

// Indexer searches Knaben.
type Indexer struct{ indexer.Base }

var _ indexer.Driver = Indexer{}

// New builds one at baseURL.
func New(client *httpx.Client, baseURL string) Indexer {
	caps := indexer.StaticCaps(map[string][]string{
		"search": {"q"}, "tv-search": {"q", "season", "ep"}, "movie-search": {"q"}, "music-search": {"q"}, "book-search": {"q"},
	}, 2000, 3000, 5000, 5070, 7000)
	return Indexer{Base: indexer.NewBase(client, baseURL, caps)}
}

type query struct {
	SearchType     string `json:"search_type"`
	SearchField    string `json:"search_field"`
	Query          string `json:"query,omitempty"`
	OrderBy        string `json:"order_by"`
	OrderDirection string `json:"order_direction"`
	Size           int    `json:"size"`
	HideUnsafe     bool   `json:"hide_unsafe"`
	HideXXX        bool   `json:"hide_xxx"`
}

type hit struct {
	Title      string    `json:"title"`
	Hash       string    `json:"hash"`
	MagnetURL  string    `json:"magnetUrl"`
	Link       string    `json:"link"`
	Details    string    `json:"details"`
	Seeders    int       `json:"seeders"`
	Peers      int       `json:"peers"`
	Bytes      int64     `json:"bytes"`
	Date       time.Time `json:"date"`
	Categories []int     `json:"categoryId"`
}

// Search asks Knaben; no words reads the newest, best seeded first otherwise.
func (i Indexer) Search(ctx context.Context, request indexer.SearchRequest) ([]indexer.RawItem, error) {
	ask := query{
		SearchType: "100%", SearchField: "title", Query: indexer.Text(request), OrderBy: "seeders", OrderDirection: "desc",
		Size: 100, HideUnsafe: true, HideXXX: true,
	}
	if ask.Query == "" {
		ask.OrderBy = "date"
	}
	var answer struct {
		Hits []hit `json:"hits"`
	}
	if err := i.PostJSON(ctx, i.BaseURL+"/v1", ask, &answer); err != nil {
		return nil, err
	}
	items := make([]indexer.RawItem, 0, len(answer.Hits))
	for _, found := range answer.Hits {
		link := found.MagnetURL
		if link == "" {
			link = found.Link
		}
		guid := found.Details
		if guid == "" {
			guid = link
		}
		var mapped []int
		for _, id := range found.Categories {
			mapped = append(mapped, category(id))
		}
		items = append(items, indexer.RawItem{
			Title: found.Title, GUID: guid, Link: link, Categories: mapped, SizeBytes: found.Bytes, PublishedAt: found.Date,
			Seeders: found.Seeders, Peers: found.Seeders + found.Peers, InfoHash: strings.ToLower(found.Hash),
		})
	}
	return indexer.Keep(request, items), nil
}

// subcategories are Knaben's own that map onto a specific Newznab one; the rest go by their million.
var subcategories = map[int]int{1001000: 3010, 1002000: 3040, 1003000: 3030, 2001000: 5040, 2003000: 5045, 3001000: 2040, 3003000: 2045}

func category(id int) int {
	if mapped, found := subcategories[id]; found {
		return mapped
	}
	switch id / 1000000 {
	case 1:
		return 3000
	case 2:
		return 5000
	case 3:
		return 2000
	case 6:
		return 5070
	case 9:
		return 7000
	}
	return 8000
}
