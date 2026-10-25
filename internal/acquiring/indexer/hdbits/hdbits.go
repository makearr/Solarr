// Package hdbits is Prowlarr's native HDBits indexer: the private high-definition tracker's JSON API, signed in with
// the user name and passkey.
package hdbits

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// Definition is HDBits as the catalog lists it.
func Definition() indexer.Definition {
	return indexer.Definition{
		ID: "hdbits", Name: "HDBits", Impl: indexer.ImplHDBits, Privacy: indexer.PrivacyPrivate, Language: "en-US",
		BaseURLs: []string{"https://hdbits.org"}, MediaTypes: []string{indexer.MediaTV, indexer.MediaMovies, indexer.MediaMusic},
		Credentials: indexer.Credentials{UserLabel: "User name", SecretLabel: "Passkey"},
		Description: "HDBits is a private high-definition movie and TV tracker; it needs the user name and passkey.",
	}
}

// Indexer searches HDBits.
type Indexer struct {
	indexer.Base
	username string
	passkey  string
}

var _ indexer.Driver = Indexer{}

// New builds one; credentials are "user:passkey".
func New(client *httpx.Client, baseURL, credentials string) Indexer {
	user, passkey := indexer.SplitCredentials(credentials)
	caps := indexer.StaticCaps(map[string][]string{
		"search": {"q"}, "tv-search": {"q", "tvdbid", "season", "ep"}, "movie-search": {"q", "imdbid"},
	}, 2000, 3000, 5000)
	return Indexer{Base: indexer.NewBase(client, baseURL, caps), username: user, passkey: passkey}
}

type query struct {
	Username string    `json:"username"`
	Passkey  string    `json:"passkey"`
	Search   string    `json:"search,omitempty"`
	Limit    int       `json:"limit"`
	IMDb     *idSearch `json:"imdb,omitempty"`
	TVDB     *idSearch `json:"tvdb,omitempty"`
}

type idSearch struct {
	ID      int `json:"id"`
	Season  int `json:"season,omitempty"`
	Episode int `json:"episode,omitempty"`
}

type answer struct {
	Status  int       `json:"status"`
	Message string    `json:"message"`
	Data    []torrent `json:"data"`
}

type torrent struct {
	ID       int64  `json:"id"`
	Hash     string `json:"hash"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Seeders  int    `json:"seeders"`
	Leechers int    `json:"leechers"`
	Added    string `json:"added"`
	Category int    `json:"type_category"`
}

func (i Indexer) call(ctx context.Context, ask query) ([]torrent, error) {
	ask.Username, ask.Passkey = i.username, i.passkey
	var parsed answer
	if err := i.PostJSON(ctx, i.BaseURL+"/api/torrents", ask, &parsed); err != nil {
		return nil, err
	}
	if parsed.Status != 0 {
		return nil, fmt.Errorf("hdbits: status %d: %s", parsed.Status, parsed.Message)
	}
	return parsed.Data, nil
}

// Capabilities proves the user name and passkey with one torrent.
func (i Indexer) Capabilities(ctx context.Context) (indexer.Capabilities, error) {
	if _, err := i.call(ctx, query{Limit: 1}); err != nil {
		return indexer.Capabilities{}, err
	}
	return i.Caps, nil
}

// Search asks by IMDb or TVDB id when the request has one, else by words.
func (i Indexer) Search(ctx context.Context, request indexer.SearchRequest) ([]indexer.RawItem, error) {
	ask := query{Limit: 100}
	season, _ := strconv.Atoi(request.Extra["season"])
	episode, _ := strconv.Atoi(request.Extra["ep"])
	if imdb, err := strconv.Atoi(strings.TrimPrefix(request.Extra["imdbid"], "tt")); err == nil {
		ask.IMDb = &idSearch{ID: imdb}
	} else if tvdb, err := strconv.Atoi(request.Extra["tvdbid"]); err == nil {
		ask.TVDB = &idSearch{ID: tvdb, Season: season, Episode: episode}
	} else {
		ask.Search = indexer.Text(request)
	}
	torrents, err := i.call(ctx, ask)
	if err != nil {
		return nil, err
	}
	items := make([]indexer.RawItem, 0, len(torrents))
	for _, found := range torrents {
		published, _ := time.Parse("2006-01-02T15:04:05-0700", found.Added)
		id := strconv.FormatInt(found.ID, 10)
		items = append(items, indexer.RawItem{
			Title: found.Name, GUID: i.BaseURL + "/details.php?id=" + id,
			Link:       i.BaseURL + "/download.php?" + url.Values{"id": {id}, "passkey": {i.passkey}}.Encode(),
			Categories: []int{category(found.Category)}, SizeBytes: found.Size, PublishedAt: published.UTC(),
			Seeders: found.Seeders, Peers: found.Seeders + found.Leechers, InfoHash: strings.ToLower(found.Hash),
		})
	}
	return indexer.Keep(request, items), nil
}

// categories are HDBits' own: 1 Movie, 2 TV, 3 Documentary, 4 Music, 5 Sport, 6 Audio Track.
var categories = map[int]int{1: 2000, 2: 5000, 3: 2000, 4: 3000, 5: 5060, 6: 3000}

func category(id int) int {
	if mapped, found := categories[id]; found {
		return mapped
	}
	return 8000
}
