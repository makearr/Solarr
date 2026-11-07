package movie

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"log/slog"
	"path"
	"strings"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/works"
	"github.com/makearr/Solarr/internal/moving/sidecar"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// ServerFiles writes what Jellyfin and Kodi read beside a movie: an nfo named after each file, and the poster and fanart
// in the movie's folder. A file already there is never replaced, and nothing is written on a root that is read only,
// offline or not writable.
type ServerFiles struct {
	Sidecar  sidecar.Writer
	Reader   library.SeriesReader
	Physical library.Physical
	Identity library.Identity
	Roots    interface {
		RootFolder(id int64) (settings.RootFolder, bool)
	}
	Clock clock.Clock
}

// artworkFiles are the movie pictures by kind, under the names both servers read in a movie folder.
var artworkFiles = []struct{ kind, name string }{
	{"poster", "poster"}, {"fanart", "fanart"}, {"logo", "clearlogo"}, {"clearart", "clearart"}, {"disc", "disc"}, {"banner", "banner"},
}

type movieNFO struct {
	XMLName       xml.Name           `xml:"movie"`
	Title         string             `xml:"title"`
	OriginalTitle string             `xml:"originaltitle,omitempty"`
	Year          int64              `xml:"year,omitempty"`
	Tagline       string             `xml:"tagline,omitempty"`
	Plot          string             `xml:"plot,omitempty"`
	Runtime       int64              `xml:"runtime,omitempty"`
	Studio        string             `xml:"studio,omitempty"`
	Genres        []string           `xml:"genre"`
	UniqueIDs     []sidecar.UniqueID `xml:"uniqueid"`
}

func movieDocument(detail library.SeriesDetailRow) ([]byte, error) {
	nfo := movieNFO{
		Title: detail.Summary.Title, OriginalTitle: detail.Summary.Fields["original_title"], Year: detail.Summary.Year,
		Tagline: detail.Summary.Fields["tagline"], Plot: detail.Summary.Description, Runtime: detail.RuntimeMs / 60_000,
		Studio: detail.Summary.Fields["studio"], Genres: detail.Summary.Genres,
	}
	for _, id := range []struct{ provider, kind string }{{ProviderTMDB, "tmdb"}, {ProviderIMDb, "imdb"}} {
		if value := detail.IDs[id.provider]; value != "" {
			nfo.UniqueIDs = append(nfo.UniqueIDs, sidecar.UniqueID{Type: id.kind, Default: len(nfo.UniqueIDs) == 0, Value: value})
		}
	}
	return sidecar.Document(nfo)
}

// WriteMovie writes the movie's missing files into each writable root its files are on, answering how many it wrote.
func (w ServerFiles) WriteMovie(ctx context.Context, categoryID, workID int64) (int, error) {
	detail, err := w.Reader.GetOf(ctx, MediaType, categoryID, workID, w.Clock.Now())
	if err != nil {
		return 0, err
	}
	locations, err := w.Identity.WorkFileRoots(ctx, workID)
	if err != nil {
		return 0, err
	}
	var images []sidecar.Image
	if encoded := detail.Summary.Fields["images"]; encoded != "" {
		_ = json.Unmarshal([]byte(encoded), &images)
	}
	written := 0
	seen := map[int64]bool{}
	for _, location := range locations {
		if seen[location.RootFolderID] {
			continue
		}
		seen[location.RootFolderID] = true
		root, found := w.Roots.RootFolder(location.RootFolderID)
		if !found || !sidecar.WritableRoot(root) {
			continue
		}
		files, err := w.Physical.PathsForWorkOnRoot(ctx, root.ID, workID)
		if err != nil {
			return written, err
		}
		folders := map[string]bool{}
		for _, file := range files {
			stem := strings.TrimSuffix(file.RelPath, path.Ext(file.RelPath))
			made, err := w.Sidecar.Once(path.Join(root.Path, stem+".nfo"), func() ([]byte, error) { return movieDocument(detail) })
			if err != nil {
				return written, err
			}
			written += made
			if folder := path.Dir(file.RelPath); folder != "." && !folders[folder] {
				folders[folder] = true
				written += w.writeArtwork(ctx, path.Join(root.Path, folder), images)
			}
		}
	}
	return written, nil
}

// writeArtwork writes each kind of picture the movie has and the folder lacks; one that cannot be had is skipped.
func (w ServerFiles) writeArtwork(ctx context.Context, folder string, images []sidecar.Image) int {
	written := 0
	for _, file := range artworkFiles {
		index := sidecar.FirstImage(images, file.kind)
		if index < 0 || w.Sidecar.AnyExists(folder, file.name) {
			continue
		}
		written += w.Sidecar.WritePicture(ctx, folder, file.name, images[index].URL)
	}
	return written
}

// ServerFilesJobType is the registered type of the pass that writes Jellyfin and Kodi files for movies.
const ServerFilesJobType = jobs.Type("movie.media_server_files")

// ServerFilesDefinition declares the pass.
var ServerFilesDefinition = works.ServerFilesDefinition("Writing Jellyfin and Kodi files for movies", ServerFilesJobType)

// ServerFilesJob writes the missing files of every movie in each movie library that asks for them.
type ServerFilesJob struct {
	Writer ServerFiles
	// Libraries answers every library, with the roots each is on.
	Libraries func() []settings.Category
	RootsOf   func(categoryID int64) []settings.RootFolder
	Logger    *slog.Logger
}

// Run implements jobs.Handler.
func (j ServerFilesJob) Run(ctx context.Context, job jobs.Job) error {
	return works.ServerFilesPass{
		MediaType: MediaType, Noun: "movie", Reader: j.Writer.Reader, Write: j.Writer.WriteMovie,
		Libraries: j.Libraries, RootsOf: j.RootsOf, Logger: j.Logger,
	}.Run(ctx, job)
}

// EnqueueServerFilesFor submits a pass for one movie, as right after an import.
func EnqueueServerFilesFor(ctx context.Context, queue *jobs.Queue, categoryID, workID int64) (jobs.EnqueueResult, error) {
	return works.EnqueueServerFilesFor(ctx, queue, ServerFilesJobType, categoryID, workID)
}

// EnqueueServerFiles submits one pass over every movie.
func EnqueueServerFiles(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return works.EnqueueServerFiles(ctx, queue, ServerFilesJobType)
}
