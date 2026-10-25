// Package soulseek searches the Soulseek network through slskd's search API (Tubifarry's way for Lidarr): a search is
// started, awaited and read, and each peer's folder becomes one release. Its link carries the folder's files as a
// download.DirectRelease, which slskd as a download client fetches; nothing is downloaded from an indexer.
package soulseek

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// Definition is Soulseek as the catalog lists it.
func Definition() indexer.Definition {
	return indexer.Definition{
		ID: "soulseek", Name: "Soulseek (slskd)", Impl: indexer.ImplSoulseek, Privacy: indexer.PrivacySemiPrivate,
		Language: "en-US", BaseURLs: []string{"http://slskd:5030"}, MediaTypes: []string{indexer.MediaMusic},
		Credentials:         indexer.Credentials{SecretLabel: "API key"},
		CompanionDownloader: "slskd",
		Description: "Searches the Soulseek peer-to-peer network through your slskd; its releases download through slskd as a " +
			"download client.",
	}
}

// searchTimeout is how long slskd collects answers; it stays well inside the 20 seconds every search waits (ADR-37).
const searchTimeout = 12 * time.Second

// pollEvery is how often a running search is asked whether it is done.
const pollEvery = time.Second

// linkScheme marks a link that is the release itself rather than an address.
const linkScheme = "slskd-release:"

// audioExtensions are the files that make a folder an album.
var audioExtensions = []string{"flac", "mp3", "m4a", "aac", "ogg", "opus", "wav", "aiff", "alac", "ape", "wv"}

// Indexer searches Soulseek through one slskd.
type Indexer struct {
	indexer.Base
	clock clock.Clock
}

var _ indexer.Driver = Indexer{}

// New builds one at slskd's address with one of its API keys.
func New(client *httpx.Client, clk clock.Clock, baseURL, apiKey string) Indexer {
	caps := indexer.StaticCaps(map[string][]string{"search": {"q"}, "music-search": {"q"}, "audio-search": {"q"}}, 3000)
	base := indexer.NewBase(client, baseURL, caps)
	base.Header = http.Header{"X-API-Key": {apiKey}}
	return Indexer{Base: base, clock: clk}
}

// Capabilities proves the address and the key.
func (i Indexer) Capabilities(ctx context.Context) (indexer.Capabilities, error) {
	var application struct {
		Version json.RawMessage `json:"version"`
	}
	if err := i.GetJSON(ctx, i.BaseURL+"/api/v0/application", &application); err != nil {
		return indexer.Capabilities{}, err
	}
	return i.Caps, nil
}

type search struct {
	ID         string `json:"id"`
	IsComplete bool   `json:"isComplete"`
}

type response struct {
	Username          string `json:"username"`
	HasFreeUploadSlot bool   `json:"hasFreeUploadSlot"`
	QueueLength       int    `json:"queueLength"`
	Files             []file `json:"files"`
}

type file struct {
	Filename  string `json:"filename"`
	Size      int64  `json:"size"`
	Extension string `json:"extension"`
	BitRate   int    `json:"bitRate"`
	BitDepth  int    `json:"bitDepth"`
	IsLocked  bool   `json:"isLocked"`
}

// Search runs one Soulseek search; an empty one (an RSS sync) and a request for anything but music answer none.
func (i Indexer) Search(ctx context.Context, request indexer.SearchRequest) ([]indexer.RawItem, error) {
	text := indexer.Text(request)
	if text == "" || !indexer.Wants(request.Categories, []int{3000}) {
		return nil, nil
	}
	id, err := newID()
	if err != nil {
		return nil, err
	}
	started := map[string]any{
		"id": id, "searchText": text, "searchTimeout": searchTimeout.Milliseconds(), "responseLimit": 100,
		"fileLimit": 10000, "filterResponses": true, "minimumResponseFileCount": 1,
	}
	var running search
	if err := i.PostJSON(ctx, i.BaseURL+"/api/v0/searches", started, &running); err != nil {
		return nil, fmt.Errorf("soulseek: start search: %w", err)
	}
	defer i.forget(ctx, id)
	for !running.IsComplete {
		if err := i.clock.Sleep(ctx, pollEvery); err != nil {
			break // out of time: what has arrived so far is still read
		}
		if err := i.GetJSON(ctx, i.BaseURL+"/api/v0/searches/"+id, &running); err != nil {
			return nil, fmt.Errorf("soulseek: search state: %w", err)
		}
	}
	var responses []response
	read := ctx
	if ctx.Err() != nil {
		var cancel context.CancelFunc
		read, cancel = context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
	}
	if err := i.GetJSON(read, i.BaseURL+"/api/v0/searches/"+id+"/responses", &responses); err != nil {
		return nil, fmt.Errorf("soulseek: search responses: %w", err)
	}
	return indexer.Keep(request, Releases(responses, i.clock.Now())), nil
}

// forget deletes the finished search from slskd, so its searches page does not fill up; a failure is only clutter.
func (i Indexer) forget(ctx context.Context, id string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	_, _ = i.Do(ctx, http.MethodDelete, i.BaseURL+"/api/v0/searches/"+id, nil, "")
}

// Releases groups each peer's files by folder: one release per folder that holds audio.
func Releases(responses []response, at time.Time) []indexer.RawItem {
	var items []indexer.RawItem
	for _, peer := range responses {
		folders := map[string][]file{}
		var order []string
		for _, found := range peer.Files {
			if found.IsLocked {
				continue
			}
			folder := folderOf(found.Filename)
			if _, seen := folders[folder]; !seen {
				order = append(order, folder)
			}
			folders[folder] = append(folders[folder], found)
		}
		for _, folder := range order {
			files := folders[folder]
			format := formatOf(files)
			if format == "" {
				continue
			}
			release := download.DirectRelease{Source: peer.Username}
			var size int64
			for _, found := range files {
				release.Files = append(release.Files, download.DirectFile{Path: found.Filename, Size: found.Size})
				size += found.Size
			}
			encoded, err := json.Marshal(release)
			if err != nil {
				continue
			}
			category := 3010
			if strings.HasPrefix(format, "FLAC") {
				category = 3040
			}
			seeders := 0
			if peer.HasFreeUploadSlot {
				seeders = 1
			}
			items = append(items, indexer.RawItem{
				Title: titleOf(folder, format), GUID: "slsk://" + peer.Username + "/" + strings.ReplaceAll(folder, `\`, "/"),
				Link:       linkScheme + base64.RawURLEncoding.EncodeToString(encoded),
				Categories: []int{category}, SizeBytes: size, PublishedAt: at, Seeders: seeders, Peers: seeders + peer.QueueLength,
			})
		}
	}
	return items
}

// Fetch answers the release a result's link carries; slskd fetches the files itself.
func (i Indexer) Fetch(_ context.Context, link string) ([]byte, error) {
	encoded, found := strings.CutPrefix(link, linkScheme)
	if !found {
		return nil, errors.New("soulseek: the link is not a Soulseek release")
	}
	release, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("soulseek: read release: %w", err)
	}
	return release, nil
}

func folderOf(filename string) string {
	cut := strings.LastIndexAny(filename, `\/`)
	if cut < 0 {
		return ""
	}
	return filename[:cut]
}

// discFolder is a folder named for one disc of an album, whose parent names the album.
func discFolder(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	for _, prefix := range []string{"cd", "disc", "disk"} {
		if rest, found := strings.CutPrefix(lower, prefix); found && strings.TrimLeft(strings.TrimSpace(rest), "0123456789") == "" {
			return true
		}
	}
	return false
}

// titleOf names a folder's release as an *Arr title, "Artist - Album (2001) [FLAC]", from its last folders.
func titleOf(folder, format string) string {
	parts := strings.FieldsFunc(folder, func(r rune) bool { return r == '\\' || r == '/' })
	if len(parts) > 1 && discFolder(parts[len(parts)-1]) {
		parts = parts[:len(parts)-1]
	}
	title := path.Base(strings.Join(parts, "/"))
	if len(parts) > 1 && !strings.Contains(title, " - ") {
		title = parts[len(parts)-2] + " - " + title
	}
	if !strings.Contains(strings.ToUpper(title), strings.Fields(format)[0]) {
		title += " [" + format + "]"
	}
	return title
}

// formatOf names a folder's audio as a release title would: "FLAC", "FLAC 24bit", "MP3 320"; empty without audio.
func formatOf(files []file) string {
	counts := map[string]int{}
	best := ""
	bitDepth, bitRate := 0, 0
	for _, found := range files {
		extension := strings.ToLower(strings.TrimPrefix(found.Extension, "."))
		if extension == "" {
			extension = strings.ToLower(strings.TrimPrefix(path.Ext(strings.ReplaceAll(found.Filename, `\`, "/")), "."))
		}
		if !slices.Contains(audioExtensions, extension) {
			continue
		}
		counts[extension]++
		if counts[extension] > counts[best] {
			best = extension
		}
		bitDepth, bitRate = max(bitDepth, found.BitDepth), max(bitRate, found.BitRate)
	}
	switch {
	case best == "":
		return ""
	case best == "flac" && bitDepth >= 24:
		return "FLAC 24bit"
	case best == "mp3" && bitRate > 0:
		return fmt.Sprintf("MP3 %d", bitRate)
	}
	return strings.ToUpper(best)
}

func newID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80
	text := hex.EncodeToString(raw)
	return text[0:8] + "-" + text[8:12] + "-" + text[12:16] + "-" + text[16:20] + "-" + text[20:], nil
}
