// Package subsplease is Prowlarr's native SubsPlease indexer: the group's own anime releases through its JSON API,
// one result per episode and resolution.
package subsplease

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// Definition is SubsPlease as the catalog lists it.
func Definition() indexer.Definition {
	return indexer.Definition{
		ID: "subsplease", Name: "SubsPlease", Impl: indexer.ImplSubsPlease, Privacy: indexer.PrivacyPublic,
		Language: "en-US", BaseURLs: []string{"https://subsplease.org"}, MediaTypes: []string{indexer.MediaAnime},
		Description: "SubsPlease's own subtitled anime releases, as soon as each episode airs.",
	}
}

// Indexer searches SubsPlease.
type Indexer struct{ indexer.Base }

var _ indexer.Driver = Indexer{}

// New builds one at baseURL.
func New(client *httpx.Client, baseURL string) Indexer {
	caps := indexer.StaticCaps(map[string][]string{"search": {"q"}, "tv-search": {"q"}}, 5070)
	return Indexer{Base: indexer.NewBase(client, baseURL, caps)}
}

type episode struct {
	Show        string `json:"show"`
	Episode     string `json:"episode"`
	ReleaseDate string `json:"release_date"`
	Page        string `json:"page"`
	Downloads   []struct {
		Resolution string `json:"res"`
		Magnet     string `json:"magnet"`
	} `json:"downloads"`
}

// Search asks SubsPlease; no words reads its latest episodes. A request for anything but anime answers none.
func (i Indexer) Search(ctx context.Context, request indexer.SearchRequest) ([]indexer.RawItem, error) {
	if !indexer.Wants(request.Categories, []int{5070}) {
		return nil, nil
	}
	query := url.Values{"f": {"latest"}, "tz": {"UTC"}}
	if text := indexer.Text(request); text != "" {
		query = url.Values{"f": {"search"}, "tz": {"UTC"}, "s": {text}}
	}
	body, err := i.Do(ctx, http.MethodGet, i.BaseURL+"/api/?"+query.Encode(), nil, "")
	if err != nil {
		return nil, err
	}
	// Nothing found is an empty array; something found is an object keyed by show and episode.
	var byKey map[string]episode
	if err := json.Unmarshal(body, &byKey); err != nil {
		var none []json.RawMessage
		if json.Unmarshal(body, &none) == nil {
			return nil, nil
		}
		return nil, fmt.Errorf("subsplease: decode response: %w", err)
	}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var items []indexer.RawItem
	for _, key := range keys {
		found := byKey[key]
		released, _ := time.Parse("01/02/06", found.ReleaseDate)
		for _, file := range found.Downloads {
			items = append(items, indexer.RawItem{
				Title: fmt.Sprintf("[SubsPlease] %s - %s (%sp)", found.Show, found.Episode, file.Resolution),
				GUID:  i.BaseURL + "/shows/" + found.Page + "/#" + found.Episode + "-" + file.Resolution, Link: file.Magnet,
				Categories: []int{5070}, SizeBytes: magnetSize(file.Magnet), PublishedAt: released, Seeders: 1, Peers: 1,
			})
		}
	}
	return indexer.Keep(request, items), nil
}

// magnetSize reads a magnet's exact length (xl), which SubsPlease's links carry.
func magnetSize(magnet string) int64 {
	parsed, err := url.Parse(magnet)
	if err != nil {
		return 0
	}
	size, _ := strconv.ParseInt(parsed.Query().Get("xl"), 10, 64)
	return size
}
