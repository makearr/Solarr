// Package passthepopcorn is Prowlarr's native PassThePopcorn indexer: the private movie tracker's JSON search, signed
// in with the API user and key.
package passthepopcorn

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// Definition is PassThePopcorn as the catalog lists it.
func Definition() indexer.Definition {
	return indexer.Definition{
		ID: "passthepopcorn", Name: "PassThePopcorn", Impl: indexer.ImplPassThePopcorn, Privacy: indexer.PrivacyPrivate,
		Language: "en-US", BaseURLs: []string{"https://passthepopcorn.me"}, MediaTypes: []string{indexer.MediaMovies},
		Credentials: indexer.Credentials{UserLabel: "API user", SecretLabel: "API key"},
		Description: "PassThePopcorn (PTP) is a private movie tracker; it needs the API user and key from its security settings.",
	}
}

// Indexer searches PassThePopcorn.
type Indexer struct{ indexer.Base }

var _ indexer.Driver = Indexer{}

// New builds one; credentials are "apiuser:apikey".
func New(client *httpx.Client, baseURL, credentials string) Indexer {
	user, key := indexer.SplitCredentials(credentials)
	caps := indexer.StaticCaps(map[string][]string{"search": {"q"}, "movie-search": {"q", "imdbid"}}, 2000)
	base := indexer.NewBase(client, baseURL, caps)
	base.Header = http.Header{"ApiUser": {user}, "ApiKey": {key}}
	return Indexer{Base: base}
}

type answer struct {
	AuthKey string  `json:"AuthKey"`
	PassKey string  `json:"PassKey"`
	Movies  []movie `json:"Movies"`
}

type movie struct {
	Title    string    `json:"Title"`
	Year     string    `json:"Year"`
	Torrents []torrent `json:"Torrents"`
}

type torrent struct {
	ID          indexer.FlexInt `json:"Id"`
	ReleaseName string          `json:"ReleaseName"`
	Size        indexer.FlexInt `json:"Size"`
	Seeders     indexer.FlexInt `json:"Seeders"`
	Leechers    indexer.FlexInt `json:"Leechers"`
	UploadTime  string          `json:"UploadTime"`
	Resolution  string          `json:"Resolution"`
}

func (i Indexer) browse(ctx context.Context, search string) (answer, error) {
	query := url.Values{"action": {"advanced"}, "json": {"noredirect"}, "grouping": {"0"}}
	if search != "" {
		query.Set("searchstr", search)
	}
	var parsed answer
	err := i.GetJSON(ctx, i.BaseURL+"/torrents.php?"+query.Encode(), &parsed)
	return parsed, err
}

// Capabilities proves the API user and key with the newest movies.
func (i Indexer) Capabilities(ctx context.Context) (indexer.Capabilities, error) {
	parsed, err := i.browse(ctx, "")
	if err != nil {
		return indexer.Capabilities{}, err
	}
	if parsed.PassKey == "" {
		return indexer.Capabilities{}, fmt.Errorf("passthepopcorn: the API user and key were refused")
	}
	return i.Caps, nil
}

// Search asks by IMDb id or words; a request for anything but movies answers none.
func (i Indexer) Search(ctx context.Context, request indexer.SearchRequest) ([]indexer.RawItem, error) {
	if !indexer.Wants(request.Categories, []int{2000}) {
		return nil, nil
	}
	search := indexer.Text(request)
	if imdb := request.Extra["imdbid"]; imdb != "" {
		search = "tt" + strings.TrimPrefix(imdb, "tt")
	}
	parsed, err := i.browse(ctx, search)
	if err != nil {
		return nil, err
	}
	var items []indexer.RawItem
	for _, found := range parsed.Movies {
		for _, file := range found.Torrents {
			id := strconv.FormatInt(int64(file.ID), 10)
			published, _ := time.Parse(time.DateTime, file.UploadTime)
			category := 2040
			if file.Resolution == "2160p" {
				category = 2045
			}
			items = append(items, indexer.RawItem{
				Title: file.ReleaseName, GUID: i.BaseURL + "/torrents.php?torrentid=" + id,
				Link: i.BaseURL + "/torrents.php?" + url.Values{
					"action": {"download"}, "id": {id}, "authkey": {parsed.AuthKey}, "torrent_pass": {parsed.PassKey},
				}.Encode(),
				Categories: []int{category}, SizeBytes: int64(file.Size), PublishedAt: published.UTC(),
				Seeders: int(file.Seeders), Peers: int(file.Seeders + file.Leechers),
			})
		}
	}
	return indexer.Keep(request, items), nil
}
