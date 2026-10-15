package tv

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// ServerFiles writes what Jellyfin and Kodi read beside a series (Stage 8 T24): tvshow.nfo, a season.nfo per season
// folder, an nfo per episode file, and the series' poster, fanart, banner and logo. A file already there is never
// replaced, and nothing is written on a root that is read only, offline or not writable.
type ServerFiles struct {
	FS     fsx.FS
	Reader library.SeriesReader
	// Crossovers tells a series' own files from those of a broadcast it shares, which stay the other series' business.
	Crossovers library.Crossovers
	Roots      interface {
		RootFolder(id int64) (settings.RootFolder, bool)
	}
	// Picture answers a provider picture's bytes as the provider serves them; nil writes no artwork.
	Picture func(ctx context.Context, address string) ([]byte, error)
	Clock   clock.Clock
}

// artworkFiles are the series pictures by kind, under the names both servers read in a series folder.
var artworkFiles = []struct{ kind, name string }{
	{"poster", "poster"}, {"fanart", "fanart"}, {"banner", "banner"}, {"logo", "clearlogo"},
}

var seasonFolder = regexp.MustCompile(`(?i)^(season \d+|specials)$`)

// WriteSeries writes the series' missing files into each writable root its files are on, answering how many it wrote.
func (w ServerFiles) WriteSeries(ctx context.Context, categoryID, workID int64) (int, error) {
	detail, err := w.Reader.Get(ctx, categoryID, workID, w.Clock.Now())
	if err != nil {
		return 0, err
	}
	paths, err := w.Reader.PartPaths(ctx, workID, ProviderTVDB+":episode")
	if err != nil {
		return 0, err
	}
	paths, err = w.ownPaths(ctx, workID, paths)
	if err != nil {
		return 0, err
	}
	written := 0
	seriesFolders := map[string]settings.RootFolder{}
	seasonFolders := map[string]int{}
	for _, file := range paths {
		root, found := w.Roots.RootFolder(file.RootFolderID)
		if !found || !writableRoot(root) {
			continue
		}
		directory := path.Dir(file.RelPath)
		series := directory
		season, _ := splitRef(file.MediaRef)
		if seasonFolder.MatchString(path.Base(directory)) {
			series = path.Dir(directory)
			seasonFolders[path.Join(root.Path, directory)] = season
		}
		if series != "." {
			seriesFolders[path.Join(root.Path, series)] = root
		}
		episodeNFO := path.Join(root.Path, strings.TrimSuffix(file.RelPath, path.Ext(file.RelPath))+".nfo")
		made, err := w.writeOnce(episodeNFO, func() ([]byte, error) { return episodeDocument(detail, file) })
		if err != nil {
			return written, err
		}
		written += made
	}
	for folder, number := range seasonFolders {
		made, err := w.writeOnce(path.Join(folder, "season.nfo"), func() ([]byte, error) { return seasonDocument(number) })
		if err != nil {
			return written, err
		}
		written += made
	}
	for _, folder := range sortedKeys(seriesFolders) {
		made, err := w.writeOnce(path.Join(folder, "tvshow.nfo"), func() ([]byte, error) { return showDocument(detail) })
		if err != nil {
			return written, err
		}
		written += made
		written += w.writeArtwork(ctx, folder, detail.Summary.Fields["images"])
	}
	return written, nil
}

// ownPaths drops the files of shared episodes that lie outside the series' own folders.
func (w ServerFiles) ownPaths(ctx context.Context, workID int64, paths []library.PartPath) ([]library.PartPath, error) {
	pairs, err := w.Crossovers.Pairs(ctx, MediaType)
	if err != nil {
		return nil, err
	}
	linked := map[string]bool{}
	for _, pair := range pairs {
		if pair.WorkID == workID {
			linked[pair.MediaRef] = true
		}
	}
	if len(linked) == 0 {
		return paths, nil
	}
	homes := seriesHomes(paths, linked)
	return slices.DeleteFunc(paths, func(file library.PartPath) bool {
		return linked[file.MediaRef] && !slices.Contains(homes, seriesHome{file.RootFolderID, seriesFolderOf(file.RelPath)})
	}), nil
}

// writeArtwork writes each kind of picture the series has and the folder lacks; a picture that cannot be had is
// skipped, never the series' failure.
func (w ServerFiles) writeArtwork(ctx context.Context, folder, encoded string) int {
	if w.Picture == nil || encoded == "" {
		return 0
	}
	var images []Image
	if json.Unmarshal([]byte(encoded), &images) != nil {
		return 0
	}
	written := 0
	for _, file := range artworkFiles {
		index := firstImage(images, file.kind)
		if index < 0 || w.anyExists(folder, file.name) {
			continue
		}
		address := images[index].URL
		extension := strings.ToLower(path.Ext(strings.SplitN(address, "?", 2)[0]))
		if extension != ".png" {
			extension = ".jpg"
		}
		made, err := w.writeOnce(path.Join(folder, file.name+extension), func() ([]byte, error) { return w.Picture(ctx, address) })
		if err == nil {
			written += made
		}
	}
	return written
}

// anyExists says whether a picture of the name is there in either format a person may have put it in.
func (w ServerFiles) anyExists(folder, name string) bool {
	for _, extension := range []string{".jpg", ".jpeg", ".png", ".webp"} {
		if _, err := w.FS.Stat(path.Join(folder, name+extension)); err == nil {
			return true
		}
	}
	return false
}

// writeOnce writes the file when nothing is at the path, answering 1 when it did.
func (w ServerFiles) writeOnce(target string, content func() ([]byte, error)) (int, error) {
	if _, err := w.FS.Stat(target); err == nil {
		return 0, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return 0, err
	}
	body, err := content()
	if err != nil {
		return 0, err
	}
	if err := fsx.WriteAtomic(w.FS, target, func(out io.Writer) error {
		_, err := out.Write(body)
		return err
	}); err != nil {
		return 0, fmt.Errorf("tv: write %s: %w", target, err)
	}
	return 1, nil
}

func writableRoot(root settings.RootFolder) bool {
	return root.Online && root.Writable && !root.ReadOnly
}

func firstImage(images []Image, kind string) int {
	for index, image := range images {
		if image.Kind == kind && image.URL != "" {
			return index
		}
	}
	return -1
}

func sortedKeys(folders map[string]settings.RootFolder) []string {
	keys := make([]string, 0, len(folders))
	for key := range folders {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// uniqueID is one provider id as both servers read it.
type uniqueID struct {
	Type    string `xml:"type,attr"`
	Default bool   `xml:"default,attr,omitempty"`
	Value   string `xml:",chardata"`
}

type showNFO struct {
	XMLName   xml.Name   `xml:"tvshow"`
	Title     string     `xml:"title"`
	Year      int64      `xml:"year,omitempty"`
	Plot      string     `xml:"plot,omitempty"`
	Status    string     `xml:"status,omitempty"`
	Studio    string     `xml:"studio,omitempty"`
	Genres    []string   `xml:"genre"`
	UniqueIDs []uniqueID `xml:"uniqueid"`
}

type seasonNFO struct {
	XMLName xml.Name `xml:"season"`
	Number  int      `xml:"seasonnumber"`
	Title   string   `xml:"title"`
}

type episodeNFO struct {
	XMLName   xml.Name   `xml:"episodedetails"`
	Title     string     `xml:"title"`
	ShowTitle string     `xml:"showtitle"`
	Season    int        `xml:"season"`
	Episode   int        `xml:"episode"`
	Aired     string     `xml:"aired,omitempty"`
	Runtime   int64      `xml:"runtime,omitempty"`
	UniqueIDs []uniqueID `xml:"uniqueid"`
}

// providerOrder is the id types a server is offered, the first one held being the default.
var providerOrder = []struct{ provider, kind string }{
	{ProviderTVDB, "tvdb"}, {ProviderTMDB, "tmdb"}, {ProviderIMDb, "imdb"}, {ProviderTVmaze, "tvmaze"},
}

func showDocument(detail library.SeriesDetailRow) ([]byte, error) {
	show := showNFO{
		Title: detail.Summary.Title, Year: detail.Summary.Year, Plot: detail.Summary.Description,
		Status: detail.Summary.Fields["status"], Studio: detail.Summary.Fields["network"], Genres: detail.Summary.Genres,
	}
	for _, id := range providerOrder {
		if value := detail.IDs[id.provider]; value != "" {
			show.UniqueIDs = append(show.UniqueIDs, uniqueID{Type: id.kind, Default: len(show.UniqueIDs) == 0, Value: value})
		}
	}
	return document(show)
}

func seasonDocument(number int) ([]byte, error) {
	title := "Season " + strconv.Itoa(number)
	if number == 0 {
		title = "Specials"
	}
	return document(seasonNFO{Number: number, Title: title})
}

func episodeDocument(detail library.SeriesDetailRow, file library.PartPath) ([]byte, error) {
	season, number := splitRef(file.MediaRef)
	episode := episodeNFO{
		Title: file.Title, ShowTitle: detail.Summary.Title, Season: season, Episode: number, Aired: file.Date,
		Runtime: file.RuntimeMs / 60_000,
	}
	if file.ExternalID != "" {
		episode.UniqueIDs = []uniqueID{{Type: "tvdb", Default: true, Value: file.ExternalID}}
	}
	return document(episode)
}

func document(value any) ([]byte, error) {
	var out bytes.Buffer
	out.WriteString(xml.Header)
	encoder := xml.NewEncoder(&out)
	encoder.Indent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	out.WriteString("\n")
	return out.Bytes(), nil
}

// ServerFilesJobType is the registered type of the pass that writes Jellyfin and Kodi files.
const ServerFilesJobType = jobs.Type("tv.media_server_files")

// ServerFilesDefinition declares the pass: housekeeping, never in front of anything a person waits on.
var ServerFilesDefinition = jobs.Definition{
	Name: "Writing Jellyfin and Kodi files",
	Type: ServerFilesJobType, Pool: jobs.PoolHousekeeping, Mutating: true, MaxAttempts: 2,
}

// ServerFilesJob writes the missing files of every series in each TV library that asks for them.
type ServerFilesJob struct {
	Writer ServerFiles
	// Libraries answers every library, with the roots each is on.
	Libraries func() []settings.Category
	RootsOf   func(categoryID int64) []settings.RootFolder
	Logger    *slog.Logger
}

// Run implements jobs.Handler.
func (j ServerFilesJob) Run(ctx context.Context, _ jobs.Job) error {
	written, series := 0, 0
	for _, category := range j.Libraries() {
		if category.MediaType != MediaType || !category.FileManagement.MediaServerFiles {
			continue
		}
		for _, root := range j.RootsOf(category.ID) {
			if !writableRoot(root) {
				continue
			}
			var afterID int64
			for {
				works, err := j.Writer.Reader.WorksOnRoot(ctx, root.ID, afterID, serverFilesPage)
				if err != nil {
					return err
				}
				for _, workID := range works {
					afterID = workID
					made, err := j.Writer.WriteSeries(ctx, category.ID, workID)
					if err != nil {
						j.Logger.Warn("jellyfin/kodi files of a series failed", slog.Int64("work_id", workID), slog.Any("error", err))
						continue
					}
					written, series = written+made, series+1
				}
				if len(works) < serverFilesPage {
					break
				}
			}
		}
	}
	j.Logger.Info("jellyfin and kodi files written", slog.Int("files", written), slog.Int("series", series))
	return nil
}

const serverFilesPage = 200

// EnqueueServerFiles submits one pass; a second while one waits is the same work.
func EnqueueServerFiles(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{
		Type: ServerFilesJobType, ActionKey: string(ServerFilesJobType), Priority: jobs.PriorityLow, Payload: []byte("{}"),
	})
}
