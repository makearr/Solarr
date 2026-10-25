// Package yts is Prowlarr's native YTS indexer: small movie encodes through the site's JSON API, searched by title
// or IMDb id, each torrent named as Prowlarr names it so its quality parses.
package yts

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// Definition is YTS as the catalog lists it.
func Definition() indexer.Definition {
	return indexer.Definition{
		ID: "yts", Name: "YTS", Impl: indexer.ImplYTS, Privacy: indexer.PrivacyPublic, Language: "en-US",
		BaseURLs: []string{"https://yts.mx"}, MediaTypes: []string{indexer.MediaMovies},
		Description: "YTS is a public tracker of small, high-definition movie encodes, through its JSON API.",
	}
}

// Indexer searches YTS.
type Indexer struct{ indexer.Base }

var _ indexer.Driver = Indexer{}

// New builds one at baseURL.
func New(client *httpx.Client, baseURL string) Indexer {
	caps := indexer.StaticCaps(map[string][]string{"search": {"q"}, "movie-search": {"q", "imdbid"}}, 2000)
	return Indexer{Base: indexer.NewBase(client, baseURL, caps)}
}

type answer struct {
	Status string `json:"status"`
	Data   struct {
		Movies []movie `json:"movies"`
	} `json:"data"`
}

type movie struct {
	URL      string    `json:"url"`
	Title    string    `json:"title"`
	Year     int       `json:"year"`
	Torrents []torrent `json:"torrents"`
}

type torrent struct {
	URL      string `json:"url"`
	Hash     string `json:"hash"`
	Quality  string `json:"quality"`
	Type     string `json:"type"`
	Codec    string `json:"video_codec"`
	Seeds    int    `json:"seeds"`
	Peers    int    `json:"peers"`
	Size     int64  `json:"size_bytes"`
	Uploaded int64  `json:"date_uploaded_unix"`
}

// Search lists YTS's movies for the words or IMDb id; a request for anything but movies answers none.
func (i Indexer) Search(ctx context.Context, request indexer.SearchRequest) ([]indexer.RawItem, error) {
	if !indexer.Wants(request.Categories, []int{2000}) {
		return nil, nil
	}
	query := url.Values{"limit": {"50"}}
	term := indexer.Text(request)
	if imdb := request.Extra["imdbid"]; imdb != "" {
		term = "tt" + strings.TrimPrefix(imdb, "tt")
	}
	if term != "" {
		query.Set("query_term", term)
	}
	var parsed answer
	if err := i.GetJSON(ctx, i.BaseURL+"/api/v2/list_movies.json?"+query.Encode(), &parsed); err != nil {
		return nil, err
	}
	if parsed.Status != "ok" {
		return nil, fmt.Errorf("yts: the API answered %q", parsed.Status)
	}
	var items []indexer.RawItem
	for _, found := range parsed.Data.Movies {
		for _, file := range found.Torrents {
			category := 2040
			if file.Quality == "2160p" {
				category = 2045
			}
			items = append(items, indexer.RawItem{
				Title: Title(found, file), GUID: file.URL, Link: file.URL, Categories: []int{category},
				SizeBytes: file.Size, PublishedAt: time.Unix(file.Uploaded, 0).UTC(), Seeders: file.Seeds,
				Peers: file.Seeds + file.Peers, InfoHash: strings.ToLower(file.Hash),
			})
		}
	}
	return indexer.Keep(request, items), nil
}

// Title names a YTS torrent as Prowlarr does: "The Matrix (1999) [1080p] [BluRay] [x265] [YTS.MX]".
func Title(found movie, file torrent) string {
	source := map[string]string{"bluray": "BluRay", "web": "WEBRip"}[strings.ToLower(file.Type)]
	parts := []string{fmt.Sprintf("%s (%d)", found.Title, found.Year), "[" + file.Quality + "]"}
	if source != "" {
		parts = append(parts, "["+source+"]")
	}
	if file.Codec != "" {
		parts = append(parts, "["+file.Codec+"]")
	}
	return strings.Join(append(parts, "[YTS.MX]"), " ")
}
