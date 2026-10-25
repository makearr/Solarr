// Package soulseek searches the Soulseek network through slskd's search API (Tubifarry's way for Lidarr), as a source
// of every media type: a search is started, awaited and read; a peer's folder of audio is one release, and its films,
// episodes and books are releases of their own, a whole folder when the folder is what was searched for. Its link carries the folder's files as a
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
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// Definition is Soulseek as the catalog lists it.
func Definition() indexer.Definition {
	return indexer.Definition{
		ID: "soulseek", Name: "Soulseek (slskd)", Impl: indexer.ImplSoulseek, Privacy: indexer.PrivacySemiPrivate,
		Language: "en-US", BaseURLs: []string{"http://slskd:5030"},
		MediaTypes:          []string{indexer.MediaMusic, indexer.MediaTV, indexer.MediaMovies, indexer.MediaAnime, indexer.MediaBooks},
		Credentials:         indexer.Credentials{SecretLabel: "API key"},
		CompanionDownloader: "slskd",
		Description: "Searches the Soulseek peer-to-peer network through your slskd for music, films, TV, anime and books; " +
			"its releases download through slskd as a download client.",
	}
}

// searchTimeout is how long slskd collects answers; it stays well inside the 20 seconds every search waits (ADR-37).
const searchTimeout = 12 * time.Second

// pollEvery is how often a running search is asked whether it is done.
const pollEvery = time.Second

// linkScheme marks a link that is the release itself rather than an address.
const linkScheme = "slskd-release:"

// The files each kind of release is made of; a subtitle or a cover goes with what shares its name or folder.
var (
	audioExtensions = []string{"flac", "mp3", "m4a", "m4b", "aac", "ogg", "opus", "wav", "aiff", "alac", "ape", "wv"}
	videoExtensions = []string{"mkv", "mp4", "avi", "m4v", "mov", "wmv", "ts", "m2ts", "webm", "mpg", "mpeg"}
	bookExtensions  = []string{"epub", "pdf", "mobi", "azw3", "azw", "cbz", "cbr", "djvu", "fb2"}
)

// episodic is a name that says which episode or season it is: TV, not a film.
var episodic = regexp.MustCompile(`(?i)\bS\d{1,2}(E\d{1,4})?\b|\b\d{1,2}x\d{2}\b|\bseason\s?\d+|\bepisode\s?\d+|\s-\s\d{2,4}\b`)

// Indexer searches Soulseek through one slskd.
type Indexer struct {
	indexer.Base
	clock clock.Clock
}

var _ indexer.Driver = Indexer{}

// New builds one at slskd's address with one of its API keys.
func New(client *httpx.Client, clk clock.Clock, baseURL, apiKey string) Indexer {
	caps := indexer.StaticCaps(map[string][]string{
		"search": {"q"}, "music-search": {"q"}, "audio-search": {"q"}, "tv-search": {"q", "season", "ep"},
		"movie-search": {"q"}, "book-search": {"q"},
	}, 2000, 3000, 5000, 7000)
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

// Search runs one Soulseek search for any media type; an empty one (an RSS sync) answers none, since Soulseek has no
// feed of what is new.
func (i Indexer) Search(ctx context.Context, request indexer.SearchRequest) ([]indexer.RawItem, error) {
	text := indexer.Text(request)
	if text == "" {
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
	return indexer.Keep(request, Releases(responses, text, i.clock.Now())), nil
}

// forget deletes the finished search from slskd, so its searches page does not fill up; a failure is only clutter.
func (i Indexer) forget(ctx context.Context, id string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	_, _ = i.Do(ctx, http.MethodDelete, i.BaseURL+"/api/v0/searches/"+id, nil, "")
}

// Releases turns each peer's answer into releases: a folder of audio is one, and so is a folder of video or books
// named for what was searched (a season, a film with its extras); any other film, episode or book is one of its own,
// with the files that share its name.
func Releases(responses []response, query string, at time.Time) []indexer.RawItem {
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
			for _, group := range groups(folder, folders[folder], query) {
				if item, ok := release(peer, group, at); ok {
					items = append(items, item)
				}
			}
		}
	}
	return items
}

// group is the files of one release, its title and categories.
type group struct {
	files       []file
	title       string
	categories  []int
	wholeFolder bool
}

func groups(folder string, files []file, query string) []group {
	if format := formatOf(files); format != "" {
		category := 3010
		switch {
		case strings.HasPrefix(format, "FLAC"):
			category = 3040
		case format == "M4B":
			category = 3030
		}
		return []group{{files: files, title: titleOf(folder, format), categories: []int{category}, wholeFolder: true}}
	}
	var found []group
	for _, kind := range []struct {
		extensions []string
		categories func(name string) []int
	}{
		{videoExtensions, videoCategories},
		{bookExtensions, func(string) []int { return []int{7000} }},
	} {
		var primaries []file
		for _, one := range files {
			if slices.Contains(kind.extensions, extensionOf(one)) {
				primaries = append(primaries, one)
			}
		}
		if len(primaries) == 0 {
			continue
		}
		name := folderName(folder)
		if len(primaries) > 1 && matches(name, query) {
			found = append(found, group{files: files, title: name, categories: kind.categories(name), wholeFolder: true})
			continue
		}
		for _, primary := range primaries {
			title := stem(lastSegment(primary.Filename))
			members := []file{primary}
			for _, other := range files {
				if other.Filename != primary.Filename && stem(lastSegment(other.Filename)) == title && !slices.Contains(kind.extensions, extensionOf(other)) {
					members = append(members, other)
				}
			}
			if len(primaries) == 1 && matches(name, query) && !matches(title, query) {
				title = name // "Film (2001)\\movie.mkv": the folder names it better than the file
			}
			found = append(found, group{files: members, title: title, categories: kind.categories(title)})
		}
	}
	return found
}

// videoCategories files a name that says its episode or season under TV, and any other video under both films and
// TV: a film's name and an anime episode's often look alike.
func videoCategories(name string) []int {
	if episodic.MatchString(name) {
		return []int{5000}
	}
	return []int{2000, 5000}
}

func release(peer response, found group, at time.Time) (indexer.RawItem, bool) {
	directRelease := download.DirectRelease{Source: peer.Username, WholeFolder: found.wholeFolder}
	var size int64
	for _, one := range found.files {
		directRelease.Files = append(directRelease.Files, download.DirectFile{Path: one.Filename, Size: one.Size})
		size += one.Size
	}
	encoded, err := json.Marshal(directRelease)
	if err != nil {
		return indexer.RawItem{}, false
	}
	seeders := 0
	if peer.HasFreeUploadSlot {
		seeders = 1
	}
	guid := "slsk://" + peer.Username + "/" + strings.ReplaceAll(folderOf(found.files[0].Filename), `\`, "/")
	if !found.wholeFolder {
		guid += "/" + lastSegment(found.files[0].Filename)
	}
	return indexer.RawItem{
		Title: found.title, GUID: guid, Link: linkScheme + base64.RawURLEncoding.EncodeToString(encoded),
		Categories: found.categories, SizeBytes: size, PublishedAt: at, Seeders: seeders, Peers: seeders + peer.QueueLength,
	}, true
}

// matches reports whether every word of the query is in name, ignoring case and punctuation.
func matches(name, query string) bool {
	have := strings.Fields(normalise(name))
	words := strings.Fields(normalise(query))
	if len(words) == 0 {
		return false
	}
	for _, word := range words {
		if !slices.Contains(have, word) {
			return false
		}
	}
	return true
}

func normalise(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return ' '
	}, text)
}

// folderName is a folder's own name, its parent's for a disc folder ("Show S01\\Disc 2").
func folderName(folder string) string {
	parts := strings.FieldsFunc(folder, func(r rune) bool { return r == '\\' || r == '/' })
	if len(parts) > 1 && discFolder(parts[len(parts)-1]) {
		parts = parts[:len(parts)-1]
	}
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

func lastSegment(path string) string { return path[strings.LastIndexAny(path, `\/`)+1:] }

// stem is a file's name without its extension, nor a subtitle's language ("Film.en.srt" is "Film").
func stem(name string) string {
	if dot := strings.LastIndex(name, "."); dot > 0 {
		name = name[:dot]
	}
	if dot := strings.LastIndex(name, "."); dot > 0 && len(name)-dot <= 4 {
		name = name[:dot]
	}
	return name
}

func extensionOf(found file) string {
	extension := strings.ToLower(strings.TrimPrefix(found.Extension, "."))
	if extension == "" {
		extension = strings.ToLower(strings.TrimPrefix(path.Ext(strings.ReplaceAll(found.Filename, `\`, "/")), "."))
	}
	return extension
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
		extension := extensionOf(found)
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
