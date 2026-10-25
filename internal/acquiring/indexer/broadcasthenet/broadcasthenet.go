// Package broadcasthenet is Prowlarr's native BroadcasTheNet indexer: the private TV tracker's JSON-RPC API, signed in
// with its API key. BTN allows 150 calls an hour; Solarr's per-indexer rate limit is where that is kept.
package broadcasthenet

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// Definition is BroadcasTheNet as the catalog lists it.
func Definition() indexer.Definition {
	return indexer.Definition{
		ID: "broadcasthenet", Name: "BroadcasTheNet", Impl: indexer.ImplBroadcastheNet, Privacy: indexer.PrivacyPrivate,
		Language: "en-US", BaseURLs: []string{"https://api.broadcasthe.net"}, MediaTypes: []string{indexer.MediaTV},
		Credentials: indexer.Credentials{SecretLabel: "API key"},
		Description: "BroadcasTheNet (BTN) is a private TV tracker; it needs the API key from its security settings.",
	}
}

// Indexer searches BroadcasTheNet.
type Indexer struct {
	indexer.Base
	apiKey string
}

var _ indexer.Driver = Indexer{}

// New builds one.
func New(client *httpx.Client, baseURL, apiKey string) Indexer {
	caps := indexer.StaticCaps(map[string][]string{"search": {"q"}, "tv-search": {"q", "tvdbid", "season", "ep"}}, 5000)
	return Indexer{Base: indexer.NewBase(client, baseURL, caps), apiKey: apiKey}
}

type call struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

type answer struct {
	Result *struct {
		Torrents map[string]torrent `json:"torrents"`
	} `json:"result"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type torrent struct {
	TorrentID   indexer.FlexInt `json:"TorrentID"`
	ReleaseName string          `json:"ReleaseName"`
	Size        indexer.FlexInt `json:"Size"`
	Seeders     indexer.FlexInt `json:"Seeders"`
	Leechers    indexer.FlexInt `json:"Leechers"`
	Time        indexer.FlexInt `json:"Time"`
	Resolution  string          `json:"Resolution"`
	InfoHash    string          `json:"InfoHash"`
	DownloadURL string          `json:"DownloadURL"`
}

func (i Indexer) torrents(ctx context.Context, filter map[string]string, limit int) ([]torrent, error) {
	var parsed answer
	err := i.PostJSON(ctx, i.BaseURL+"/", call{JSONRPC: "2.0", ID: 1, Method: "getTorrents", Params: []any{i.apiKey, filter, limit, 0}}, &parsed)
	if err != nil {
		return nil, err
	}
	if parsed.Error != nil {
		return nil, fmt.Errorf("broadcasthenet: %d: %s", parsed.Error.Code, parsed.Error.Message)
	}
	if parsed.Result == nil {
		return nil, nil
	}
	ids := make([]string, 0, len(parsed.Result.Torrents))
	for id := range parsed.Result.Torrents {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	found := make([]torrent, len(ids))
	for index, id := range ids {
		found[index] = parsed.Result.Torrents[id]
	}
	return found, nil
}

// Capabilities proves the key with one torrent.
func (i Indexer) Capabilities(ctx context.Context) (indexer.Capabilities, error) {
	if _, err := i.torrents(ctx, map[string]string{}, 1); err != nil {
		return indexer.Capabilities{}, err
	}
	return i.Caps, nil
}

// Search asks by TVDB id with the season and episode, or by words; a request for anything but TV answers none.
func (i Indexer) Search(ctx context.Context, request indexer.SearchRequest) ([]indexer.RawItem, error) {
	if !indexer.Wants(request.Categories, []int{5000}) {
		return nil, nil
	}
	filter := map[string]string{}
	if tvdb := request.Extra["tvdbid"]; tvdb != "" {
		filter["tvdb"] = tvdb
		season, _ := strconv.Atoi(request.Extra["season"])
		episode, _ := strconv.Atoi(request.Extra["ep"])
		switch {
		case season > 0 && episode > 0:
			filter["category"], filter["name"] = "Episode", fmt.Sprintf("S%02dE%02d", season, episode)
		case season > 0:
			filter["category"], filter["name"] = "Season", fmt.Sprintf("Season %d", season)
		}
	} else if text := indexer.Text(request); text != "" {
		filter["search"] = text
	}
	torrents, err := i.torrents(ctx, filter, 100)
	if err != nil {
		return nil, err
	}
	items := make([]indexer.RawItem, 0, len(torrents))
	for _, found := range torrents {
		category := 5040
		if strings.EqualFold(found.Resolution, "SD") {
			category = 5030
		}
		items = append(items, indexer.RawItem{
			Title: found.ReleaseName, GUID: "https://broadcasthe.net/torrents.php?torrentid=" + strconv.FormatInt(int64(found.TorrentID), 10),
			Link: found.DownloadURL, Categories: []int{category}, SizeBytes: int64(found.Size),
			PublishedAt: time.Unix(int64(found.Time), 0).UTC(), Seeders: int(found.Seeders),
			Peers: int(found.Seeders + found.Leechers), InfoHash: strings.ToLower(found.InfoHash),
		})
	}
	return indexer.Keep(request, items), nil
}
