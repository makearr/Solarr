// Package gazelle is Prowlarr's native Redacted and Orpheus indexers: the Gazelle tracker JSON API (ajax.php), the
// two private music trackers that speak it with an API key. Each torrent of an album is named as Prowlarr names it,
// "Artist - Album (Year) [CD FLAC Lossless]", so its quality parses.
package gazelle

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// RedactedDefinition is Redacted as the catalog lists it.
func RedactedDefinition() indexer.Definition {
	return indexer.Definition{
		ID: "redacted", Name: "Redacted", Impl: indexer.ImplRedacted, Privacy: indexer.PrivacyPrivate, Language: "en-US",
		BaseURLs: []string{"https://redacted.sh"}, MediaTypes: []string{indexer.MediaMusic},
		Credentials: Credentials(),
		Description: "Redacted (RED) is a private music tracker; it needs an API key with the Torrents scope.",
	}
}

// OrpheusDefinition is Orpheus as the catalog lists it.
func OrpheusDefinition() indexer.Definition {
	return indexer.Definition{
		ID: "orpheus", Name: "Orpheus", Impl: indexer.ImplOrpheus, Privacy: indexer.PrivacyPrivate, Language: "en-US",
		BaseURLs: []string{"https://orpheus.network"}, MediaTypes: []string{indexer.MediaMusic},
		Credentials: Credentials(),
		Description: "Orpheus (OPS) is a private music tracker; it needs an API key from the user's settings.",
	}
}

// Credentials are a Gazelle API key.
func Credentials() indexer.Credentials { return indexer.Credentials{SecretLabel: "API key"} }

// Indexer searches one Gazelle tracker.
type Indexer struct{ indexer.Base }

var _ indexer.Driver = Indexer{}

// NewRedacted builds Redacted, which takes the key as the whole Authorization header.
func NewRedacted(client *httpx.Client, baseURL, apiKey string) Indexer {
	return newIndexer(client, baseURL, apiKey)
}

// NewOrpheus builds Orpheus, which takes the key as "token <key>".
func NewOrpheus(client *httpx.Client, baseURL, apiKey string) Indexer {
	return newIndexer(client, baseURL, "token "+apiKey)
}

func newIndexer(client *httpx.Client, baseURL, authorization string) Indexer {
	caps := indexer.StaticCaps(map[string][]string{
		"search": {"q"}, "music-search": {"q", "artist", "album", "year"}, "audio-search": {"q"},
	}, 3000)
	base := indexer.NewBase(client, baseURL, caps)
	base.Header = http.Header{"Authorization": {authorization}}
	return Indexer{Base: base}
}

type browse struct {
	Status   string `json:"status"`
	Error    string `json:"error"`
	Response struct {
		Results []group `json:"results"`
	} `json:"response"`
}

type group struct {
	GroupID   int64     `json:"groupId"`
	GroupName string    `json:"groupName"`
	Artist    string    `json:"artist"`
	GroupYear int       `json:"groupYear"`
	Torrents  []torrent `json:"torrents"`
}

type torrent struct {
	TorrentID     int64  `json:"torrentId"`
	Media         string `json:"media"`
	Format        string `json:"format"`
	Encoding      string `json:"encoding"`
	Remastered    bool   `json:"remastered"`
	RemasterYear  int    `json:"remasterYear"`
	RemasterTitle string `json:"remasterTitle"`
	Scene         bool   `json:"scene"`
	HasLog        bool   `json:"hasLog"`
	HasCue        bool   `json:"hasCue"`
	LogScore      int    `json:"logScore"`
	Time          string `json:"time"`
	Size          int64  `json:"size"`
	Seeders       int    `json:"seeders"`
	Leechers      int    `json:"leechers"`
}

// Capabilities proves the key with the cheapest call Gazelle has, then answers what it searches.
func (i Indexer) Capabilities(ctx context.Context) (indexer.Capabilities, error) {
	var answer struct {
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	if err := i.GetJSON(ctx, i.BaseURL+"/ajax.php?action=index", &answer); err != nil {
		return indexer.Capabilities{}, err
	}
	if answer.Status != "success" {
		return indexer.Capabilities{}, fmt.Errorf("gazelle: the tracker refused the key: %s", answer.Error)
	}
	return i.Caps, nil
}

// Search browses the tracker: by artist and album when the request names them, else by words; none reads the newest.
func (i Indexer) Search(ctx context.Context, request indexer.SearchRequest) ([]indexer.RawItem, error) {
	if !indexer.Wants(request.Categories, []int{3000}) {
		return nil, nil
	}
	query := url.Values{"action": {"browse"}, "order_by": {"time"}, "order_way": {"desc"}}
	if artist := request.Extra["artist"]; artist != "" && request.Query == "" {
		query.Set("artistname", artist)
		query.Set("groupname", request.Extra["album"])
		if year := request.Extra["year"]; year != "" {
			query.Set("year", year)
		}
	} else if text := indexer.Text(request); text != "" {
		query.Set("searchstr", text)
	}
	var answer browse
	if err := i.GetJSON(ctx, i.BaseURL+"/ajax.php?"+query.Encode(), &answer); err != nil {
		return nil, err
	}
	if answer.Status != "success" {
		return nil, fmt.Errorf("gazelle: browse: %s", answer.Error)
	}
	var items []indexer.RawItem
	for _, found := range answer.Response.Results {
		for _, file := range found.Torrents {
			published, _ := time.Parse(time.DateTime, file.Time)
			id := strconv.FormatInt(file.TorrentID, 10)
			items = append(items, indexer.RawItem{
				Title: Title(found, file), GUID: i.BaseURL + "/torrents.php?torrentid=" + id,
				Link:       i.BaseURL + "/ajax.php?action=download&id=" + id,
				Categories: []int{category(file.Format)}, SizeBytes: file.Size, PublishedAt: published.UTC(),
				Seeders: file.Seeders, Peers: file.Seeders + file.Leechers,
			})
		}
	}
	return indexer.Keep(request, items), nil
}

func category(format string) int {
	switch strings.ToUpper(format) {
	case "FLAC":
		return 3040
	case "MP3", "AAC":
		return 3010
	}
	return 3000
}

// Title names one torrent of an album as Prowlarr does; Gazelle sends its names HTML-escaped.
func Title(found group, file torrent) string {
	year := found.GroupYear
	if file.Remastered && file.RemasterYear > 0 {
		year = file.RemasterYear
	}
	title := fmt.Sprintf("%s - %s (%d) [%s %s %s]", html.UnescapeString(found.Artist), html.UnescapeString(found.GroupName),
		year, file.Media, file.Format, file.Encoding)
	if file.RemasterTitle != "" {
		title += " [" + html.UnescapeString(file.RemasterTitle) + "]"
	}
	if file.Scene {
		title += " [Scene]"
	}
	if file.HasLog {
		title += fmt.Sprintf(" [Log %d%%]", file.LogScore)
	}
	if file.HasCue {
		title += " [Cue]"
	}
	return title
}
