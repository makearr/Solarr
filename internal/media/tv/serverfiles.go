package tv

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
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
	"github.com/makearr/Solarr/internal/moving/sidecar"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// ServerFiles writes what Jellyfin and Kodi read beside a series (Stage 8 T24): tvshow.nfo, a season.nfo per season
// folder, an nfo and a thumb per episode file, the series' poster, fanart, banner and logo, and a poster per season. A file already there is never
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

func (w ServerFiles) files() sidecar.Writer { return sidecar.Writer{FS: w.FS, Picture: w.Picture} }

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
		if !found || !sidecar.WritableRoot(root) {
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
		made, err := w.files().Once(episodeNFO, func() ([]byte, error) { return episodeDocument(detail, file) })
		if err != nil {
			return written, err
		}
		written += made
		written += w.writeEpisodeThumb(ctx, path.Join(root.Path, strings.TrimSuffix(file.RelPath, path.Ext(file.RelPath))), detail.Summary.Fields["episode_images"], file.MediaRef)
	}
	for folder, number := range seasonFolders {
		made, err := w.files().Once(path.Join(folder, "season.nfo"), func() ([]byte, error) { return seasonDocument(number) })
		if err != nil {
			return written, err
		}
		written += made
	}
	for _, folder := range sortedKeys(seriesFolders) {
		made, err := w.files().Once(path.Join(folder, "tvshow.nfo"), func() ([]byte, error) { return showDocument(detail) })
		if err != nil {
			return written, err
		}
		written += made
		written += w.writeArtwork(ctx, folder, detail.Summary.Fields["images"])
	}
	written += w.writeSeasonPosters(ctx, seasonFolders, detail.Summary.Fields["season_images"])
	return written, nil
}

// writeSeasonPosters writes seasonNN-poster beside each season folder's parent, the name both servers read.
func (w ServerFiles) writeSeasonPosters(ctx context.Context, seasonFolders map[string]int, encoded string) int {
	if w.Picture == nil || encoded == "" {
		return 0
	}
	var seasons []Season
	if json.Unmarshal([]byte(encoded), &seasons) != nil {
		return 0
	}
	written := 0
	for folder, number := range seasonFolders {
		index := slices.IndexFunc(seasons, func(season Season) bool { return season.Number == number && season.Poster != "" })
		name := seasonPosterName(number)
		parent := path.Dir(folder)
		if index < 0 || w.files().AnyExists(parent, name) {
			continue
		}
		written += w.files().WritePicture(ctx, parent, name, seasons[index].Poster)
	}
	return written
}

// writeEpisodeThumb writes <episode file name>-thumb beside the episode, from the still a provider holds of it.
func (w ServerFiles) writeEpisodeThumb(ctx context.Context, base, encoded, mediaRef string) int {
	if w.Picture == nil || encoded == "" {
		return 0
	}
	var stills map[string]string
	if json.Unmarshal([]byte(encoded), &stills) != nil {
		return 0
	}
	season, number := splitRef(mediaRef)
	address := stills[stillKey(season, number)]
	name := path.Base(base) + "-thumb"
	if address == "" || w.files().AnyExists(path.Dir(base), name) {
		return 0
	}
	return w.files().WritePicture(ctx, path.Dir(base), name, address)
}

func seasonPosterName(number int) string {
	if number == 0 {
		return "season-specials-poster"
	}
	return fmt.Sprintf("season%02d-poster", number)
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
	var images []sidecar.Image
	if json.Unmarshal([]byte(encoded), &images) != nil {
		return 0
	}
	written := 0
	for _, file := range artworkFiles {
		index := sidecar.FirstImage(images, file.kind)
		if index < 0 || w.files().AnyExists(folder, file.name) {
			continue
		}
		written += w.files().WritePicture(ctx, folder, file.name, images[index].URL)
	}
	return written
}

func sortedKeys(folders map[string]settings.RootFolder) []string {
	keys := make([]string, 0, len(folders))
	for key := range folders {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

type showNFO struct {
	XMLName   xml.Name           `xml:"tvshow"`
	Title     string             `xml:"title"`
	Year      int64              `xml:"year,omitempty"`
	Plot      string             `xml:"plot,omitempty"`
	Status    string             `xml:"status,omitempty"`
	Studio    string             `xml:"studio,omitempty"`
	Genres    []string           `xml:"genre"`
	UniqueIDs []sidecar.UniqueID `xml:"uniqueid"`
}

type seasonNFO struct {
	XMLName xml.Name `xml:"season"`
	Number  int      `xml:"seasonnumber"`
	Title   string   `xml:"title"`
}

type episodeNFO struct {
	XMLName   xml.Name           `xml:"episodedetails"`
	Title     string             `xml:"title"`
	ShowTitle string             `xml:"showtitle"`
	Season    int                `xml:"season"`
	Episode   int                `xml:"episode"`
	Aired     string             `xml:"aired,omitempty"`
	Runtime   int64              `xml:"runtime,omitempty"`
	UniqueIDs []sidecar.UniqueID `xml:"uniqueid"`
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
			show.UniqueIDs = append(show.UniqueIDs, sidecar.UniqueID{Type: id.kind, Default: len(show.UniqueIDs) == 0, Value: value})
		}
	}
	return sidecar.Document(show)
}

func seasonDocument(number int) ([]byte, error) {
	title := "Season " + strconv.Itoa(number)
	if number == 0 {
		title = "Specials"
	}
	return sidecar.Document(seasonNFO{Number: number, Title: title})
}

func episodeDocument(detail library.SeriesDetailRow, file library.PartPath) ([]byte, error) {
	season, number := splitRef(file.MediaRef)
	episode := episodeNFO{
		Title: file.Title, ShowTitle: detail.Summary.Title, Season: season, Episode: number, Aired: file.Date,
		Runtime: file.RuntimeMs / 60_000,
	}
	if file.ExternalID != "" {
		episode.UniqueIDs = []sidecar.UniqueID{{Type: "tvdb", Default: true, Value: file.ExternalID}}
	}
	return sidecar.Document(episode)
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
func (j ServerFilesJob) Run(ctx context.Context, job jobs.Job) error {
	var one serverFilesPayload
	if err := json.Unmarshal(job.Payload, &one); err == nil && one.WorkID != 0 {
		return j.runOne(ctx, one)
	}
	written, series := 0, 0
	for _, category := range j.Libraries() {
		if category.MediaType != MediaType || !category.FileManagement.MediaServerFiles {
			continue
		}
		for _, root := range j.RootsOf(category.ID) {
			if !sidecar.WritableRoot(root) {
				continue
			}
			var afterID int64
			for {
				works, err := j.Writer.Reader.WorksOnRoot(ctx, string(MediaType), root.ID, afterID, serverFilesPage)
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

// serverFilesPayload names the one series a pass is for; empty is every series.
type serverFilesPayload struct {
	CategoryID int64 `json:"category_id"`
	WorkID     int64 `json:"work_id"`
}

func (j ServerFilesJob) runOne(ctx context.Context, one serverFilesPayload) error {
	for _, category := range j.Libraries() {
		if category.ID != one.CategoryID || category.MediaType != MediaType || !category.FileManagement.MediaServerFiles {
			continue
		}
		made, err := j.Writer.WriteSeries(ctx, one.CategoryID, one.WorkID)
		if err != nil {
			return err
		}
		j.Logger.Info("jellyfin and kodi files written", slog.Int("files", made), slog.Int64("work_id", one.WorkID))
	}
	return nil
}

// EnqueueServerFilesFor submits a pass for one series, as right after an import; one already waiting for it is the same work.
func EnqueueServerFilesFor(ctx context.Context, queue *jobs.Queue, categoryID, workID int64) (jobs.EnqueueResult, error) {
	payload, err := json.Marshal(serverFilesPayload{CategoryID: categoryID, WorkID: workID})
	if err != nil {
		return jobs.EnqueueResult{}, err
	}
	return queue.Enqueue(ctx, jobs.Request{
		Type: ServerFilesJobType, ActionKey: fmt.Sprintf("%s:%d", ServerFilesJobType, workID), Priority: jobs.PriorityLow, Payload: payload,
	})
}

// EnqueueServerFiles submits one pass; a second while one waits is the same work.
func EnqueueServerFiles(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{
		Type: ServerFilesJobType, ActionKey: string(ServerFilesJobType), Priority: jobs.PriorityLow, Payload: []byte("{}"),
	})
}
