// Package beyondhd is Prowlarr's native BeyondHD indexer: the private tracker's torrents API, signed in with the API
// key in the path and the RSS key in the body.
package beyondhd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// Definition is BeyondHD as the catalog lists it.
func Definition() indexer.Definition {
	return indexer.Definition{
		ID: "beyondhd", Name: "BeyondHD", Impl: indexer.ImplBeyondHD, Privacy: indexer.PrivacyPrivate, Language: "en-US",
		BaseURLs: []string{"https://beyond-hd.me"}, MediaTypes: []string{indexer.MediaTV, indexer.MediaMovies},
		Credentials: indexer.Credentials{UserLabel: "RSS key", SecretLabel: "API key"},
		Description: "BeyondHD (BHD) is a private high-definition tracker; it needs the API key and RSS key.",
	}
}

// Indexer searches BeyondHD.
type Indexer struct {
	indexer.Base
	apiKey string
	rssKey string
}

var _ indexer.Driver = Indexer{}

// New builds one; credentials are "rsskey:apikey".
func New(client *httpx.Client, baseURL, credentials string) Indexer {
	rss, key := indexer.SplitCredentials(credentials)
	caps := indexer.StaticCaps(map[string][]string{
		"search": {"q"}, "tv-search": {"q", "season", "ep", "imdbid"}, "movie-search": {"q", "imdbid"},
	}, 2000, 5000)
	return Indexer{Base: indexer.NewBase(client, baseURL, caps), apiKey: key, rssKey: rss}
}

type query struct {
	Action string `json:"action"`
	RSSKey string `json:"rsskey"`
	Search string `json:"search,omitempty"`
	IMDb   string `json:"imdb_id,omitempty"`
}

type answer struct {
	StatusCode int       `json:"status_code"`
	Success    bool      `json:"success"`
	Message    string    `json:"status_message"`
	Results    []torrent `json:"results"`
}

type torrent struct {
	Name        string `json:"name"`
	URL         string `json:"url"`
	DownloadURL string `json:"download_url"`
	InfoHash    string `json:"info_hash"`
	Category    string `json:"category"`
	Type        string `json:"type"`
	Size        int64  `json:"size"`
	Seeders     int    `json:"seeders"`
	Leechers    int    `json:"leechers"`
	CreatedAt   string `json:"created_at"`
}

func (i Indexer) call(ctx context.Context, ask query) ([]torrent, error) {
	ask.Action, ask.RSSKey = "search", i.rssKey
	var parsed answer
	if err := i.PostJSON(ctx, i.BaseURL+"/api/torrents/"+i.apiKey, ask, &parsed); err != nil {
		return nil, err
	}
	if !parsed.Success {
		return nil, fmt.Errorf("beyondhd: %s", parsed.Message)
	}
	return parsed.Results, nil
}

// Capabilities proves both keys with the newest torrents.
func (i Indexer) Capabilities(ctx context.Context) (indexer.Capabilities, error) {
	if _, err := i.call(ctx, query{}); err != nil {
		return indexer.Capabilities{}, err
	}
	return i.Caps, nil
}

// Search asks by IMDb id or words.
func (i Indexer) Search(ctx context.Context, request indexer.SearchRequest) ([]indexer.RawItem, error) {
	ask := query{Search: indexer.Text(request)}
	if imdb := request.Extra["imdbid"]; imdb != "" {
		ask = query{IMDb: "tt" + strings.TrimPrefix(imdb, "tt")}
	}
	torrents, err := i.call(ctx, ask)
	if err != nil {
		return nil, err
	}
	items := make([]indexer.RawItem, 0, len(torrents))
	for _, found := range torrents {
		published, _ := time.Parse(time.DateTime, found.CreatedAt)
		category := 2000
		if strings.EqualFold(found.Category, "TV") {
			category = 5000
		}
		items = append(items, indexer.RawItem{
			Title: found.Name, GUID: found.URL, Link: found.DownloadURL, Categories: []int{category}, SizeBytes: found.Size,
			PublishedAt: published.UTC(), Seeders: found.Seeders, Peers: found.Seeders + found.Leechers,
			InfoHash: strings.ToLower(found.InfoHash),
		})
	}
	return indexer.Keep(request, items), nil
}
