// Package internetarchive is Prowlarr's native Internet Archive indexer: the archive's advanced search over items it
// offers as torrents, live music (etree) above all.
package internetarchive

import (
	"context"
	"net/url"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// Definition is the Internet Archive as the catalog lists it.
func Definition() indexer.Definition {
	return indexer.Definition{
		ID: "internetarchive", Name: "Internet Archive", Impl: indexer.ImplInternetArchive, Privacy: indexer.PrivacyPublic,
		Language: "en-US", BaseURLs: []string{"https://archive.org"},
		MediaTypes:  []string{indexer.MediaMusic, indexer.MediaMovies, indexer.MediaBooks},
		Description: "The Internet Archive's public torrents: live recordings, public-domain films and books.",
	}
}

// Indexer searches the archive.
type Indexer struct{ indexer.Base }

var _ indexer.Driver = Indexer{}

// New builds one at baseURL.
func New(client *httpx.Client, baseURL string) Indexer {
	caps := indexer.StaticCaps(map[string][]string{
		"search": {"q"}, "music-search": {"q"}, "movie-search": {"q"}, "book-search": {"q"},
	}, 2000, 3000, 7000)
	return Indexer{Base: indexer.NewBase(client, baseURL, caps)}
}

type doc struct {
	Identifier string          `json:"identifier"`
	Title      string          `json:"title"`
	Size       indexer.FlexInt `json:"item_size"`
	Published  time.Time       `json:"publicdate"`
	MediaType  string          `json:"mediatype"`
}

// Search asks the archive for items with a torrent; no words reads the newest.
func (i Indexer) Search(ctx context.Context, request indexer.SearchRequest) ([]indexer.RawItem, error) {
	terms := `format:("Archive BitTorrent")`
	if text := indexer.Text(request); text != "" {
		terms = "(" + text + ") AND " + terms
	}
	query := url.Values{
		"q": {terms}, "fl[]": {"identifier", "title", "item_size", "publicdate", "mediatype"},
		"sort[]": {"publicdate desc"}, "rows": {"100"}, "output": {"json"},
	}
	var answer struct {
		Response struct {
			Docs []doc `json:"docs"`
		} `json:"response"`
	}
	if err := i.GetJSON(ctx, i.BaseURL+"/advancedsearch.php?"+query.Encode(), &answer); err != nil {
		return nil, err
	}
	items := make([]indexer.RawItem, 0, len(answer.Response.Docs))
	for _, found := range answer.Response.Docs {
		id := url.PathEscape(found.Identifier)
		items = append(items, indexer.RawItem{
			Title: found.Title, GUID: i.BaseURL + "/details/" + id,
			Link: i.BaseURL + "/download/" + id + "/" + id + "_archive.torrent", Categories: []int{category(found.MediaType)},
			// The archive web-seeds every torrent it offers, so one is always there.
			SizeBytes: int64(found.Size), PublishedAt: found.Published, Seeders: 1, Peers: 1,
		})
	}
	return indexer.Keep(request, items), nil
}

func category(mediaType string) int {
	switch mediaType {
	case "audio", "etree":
		return 3000
	case "movies":
		return 2000
	case "texts":
		return 7000
	}
	return 8000
}
