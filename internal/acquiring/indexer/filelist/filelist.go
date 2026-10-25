// Package filelist is Prowlarr's native FileList indexer: the private tracker's JSON API, signed in with the user
// name and passkey.
package filelist

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// Definition is FileList as the catalog lists it.
func Definition() indexer.Definition {
	return indexer.Definition{
		ID: "filelist", Name: "FileList", Impl: indexer.ImplFileList, Privacy: indexer.PrivacyPrivate, Language: "ro-RO",
		BaseURLs:    []string{"https://filelist.io"},
		MediaTypes:  []string{indexer.MediaMusic, indexer.MediaTV, indexer.MediaMovies, indexer.MediaAnime},
		Credentials: indexer.Credentials{UserLabel: "User name", SecretLabel: "Passkey"},
		Description: "FileList is a private Romanian general tracker; it needs the user name and passkey.",
	}
}

// Indexer searches FileList.
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
		"search": {"q"}, "tv-search": {"q", "season", "ep", "imdbid"}, "movie-search": {"q", "imdbid"}, "music-search": {"q"},
	}, 2000, 3000, 5000, 5070)
	return Indexer{Base: indexer.NewBase(client, baseURL, caps), username: user, passkey: passkey}
}

type torrent struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Category   string `json:"category"`
	Size       int64  `json:"size"`
	Seeders    int    `json:"seeders"`
	Leechers   int    `json:"leechers"`
	UploadDate string `json:"upload_date"`
	Link       string `json:"download_link"`
}

func (i Indexer) call(ctx context.Context, query url.Values) ([]torrent, error) {
	query.Set("username", i.username)
	query.Set("passkey", i.passkey)
	body, err := i.Do(ctx, http.MethodGet, i.BaseURL+"/api.php?"+query.Encode(), nil, "")
	if err != nil {
		return nil, err
	}
	var refused struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &refused) == nil && refused.Error != "" {
		return nil, fmt.Errorf("filelist: %s", refused.Error)
	}
	var torrents []torrent
	if err := json.Unmarshal(body, &torrents); err != nil {
		return nil, fmt.Errorf("filelist: decode response: %w", err)
	}
	return torrents, nil
}

// Capabilities proves the user name and passkey with the latest torrents.
func (i Indexer) Capabilities(ctx context.Context) (indexer.Capabilities, error) {
	if _, err := i.call(ctx, url.Values{"action": {"latest-torrents"}, "limit": {"1"}}); err != nil {
		return indexer.Capabilities{}, err
	}
	return i.Caps, nil
}

// Search asks by IMDb id or by name; no words reads the latest.
func (i Indexer) Search(ctx context.Context, request indexer.SearchRequest) ([]indexer.RawItem, error) {
	query := url.Values{"action": {"latest-torrents"}, "limit": {"100"}}
	switch imdb := request.Extra["imdbid"]; {
	case imdb != "":
		query = url.Values{"action": {"search-torrents"}, "type": {"imdb"}, "query": {"tt" + strings.TrimPrefix(imdb, "tt")}}
	case indexer.Text(request) != "":
		query = url.Values{"action": {"search-torrents"}, "type": {"name"}, "query": {indexer.Text(request)}}
	}
	torrents, err := i.call(ctx, query)
	if err != nil {
		return nil, err
	}
	items := make([]indexer.RawItem, 0, len(torrents))
	for _, found := range torrents {
		published, _ := time.Parse(time.DateTime, found.UploadDate)
		items = append(items, indexer.RawItem{
			Title: found.Name, GUID: i.BaseURL + "/details.php?id=" + strconv.FormatInt(found.ID, 10), Link: found.Link,
			Categories: []int{category(found.Category)}, SizeBytes: found.Size, PublishedAt: published.UTC(),
			Seeders: found.Seeders, Peers: found.Seeders + found.Leechers,
		})
	}
	return indexer.Keep(request, items), nil
}

// category reads FileList's category names ("Filme HD", "Seriale 4K", "Audio", "FLAC", "Anime").
func category(name string) int {
	lower := strings.ToLower(name)
	switch {
	case lower == "flac":
		return 3040
	case strings.HasPrefix(lower, "audio"), strings.Contains(lower, "videoclip"):
		return 3000
	case strings.HasPrefix(lower, "anime"):
		return 5070
	case strings.HasPrefix(lower, "seriale"):
		return 5000
	case strings.HasPrefix(lower, "filme"), strings.HasPrefix(lower, "desene"):
		return 2000
	}
	return 8000
}
