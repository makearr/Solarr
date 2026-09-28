// Package downloadwatch imports what lands in a watched download folder (2026-09-19 review):
// each top-level entry, once it has settled, exactly once.
package downloadwatch

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/importing/importjob"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// JobType is the registered type of one pass over every watched folder.
const JobType = jobs.Type("import.download_watch")

// ActionKey makes a pass global: one pass covers every folder.
const ActionKey = "import.download_watch"

// Definition runs a pass in the background; it only lists and enqueues. Acquire pool, as the poll.
var Definition = jobs.Definition{Type: JobType, Pool: jobs.PoolAcquire, Mutating: false, MaxAttempts: 3}

// DefaultSettle is how long nothing in an entry may change before it is imported, so a
// download still being written is never taken half-finished.
const DefaultSettle = 10 * time.Minute

// maxSettleEntries bounds the walk that checks an entry has settled.
const maxSettleEntries = 20000

// Folders answers the configured download folders.
type Folders interface {
	DownloadFolders(ctx context.Context) ([]settings.DownloadFolder, error)
}

// Sessions answers whether a path was ever imported.
type Sessions interface {
	HasSessionFor(ctx context.Context, sourcePath string) (bool, error)
}

// Libraries resolves "any library" to one: import places into a library, and music is the
// only media type import handles today.
type Libraries interface {
	CategoryForMediaType(mediaType string) (settings.Category, bool)
}

// Config is what a Job needs; every field is required.
type Config struct {
	Folders   Folders
	FS        fsx.FS
	Sessions  Sessions
	Libraries Libraries
	Enqueue   func(ctx context.Context, payload importjob.Payload) error
	Clock     clock.Clock
	Settle    time.Duration
	Logger    *slog.Logger
}

// Job is one pass.
type Job struct{ config Config }

// NewJob builds one.
func NewJob(config Config) *Job { return &Job{config: config} }

// Enqueue submits one pass.
func Enqueue(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{Type: JobType, ActionKey: ActionKey, Priority: jobs.PriorityLow})
}

// Run lists each watched, readable folder's top level and enqueues an import for every entry
// that has settled and was never imported. It never walks a folder beyond one entry at a time.
func (j *Job) Run(ctx context.Context, _ jobs.Job) error {
	folders, err := j.config.Folders.DownloadFolders(ctx)
	if err != nil {
		return jobs.Retryablef("download_watch.folders", string(JobType), err)
	}
	for _, folder := range folders {
		if !folder.Watch || !folder.Readable {
			continue
		}
		if err := j.watchOne(ctx, folder); err != nil {
			j.config.Logger.Warn("download folder not watched this pass",
				slog.String("path", folder.Path), slog.Any("error", err))
		}
	}
	return nil
}

func (j *Job) watchOne(ctx context.Context, folder settings.DownloadFolder) error {
	entries, err := j.config.FS.ReadDir(folder.Path)
	if err != nil {
		return err
	}
	categoryID := folder.CategoryID
	if categoryID == nil {
		category, found := j.config.Libraries.CategoryForMediaType("music")
		if !found {
			return fmt.Errorf("no library to import %s into", folder.Path)
		}
		categoryID = &category.ID
	}
	settledBefore := j.config.Clock.Now().Add(-j.config.Settle)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name, ".") {
			continue
		}
		source := path.Join(folder.Path, entry.Name)
		seen, err := j.config.Sessions.HasSessionFor(ctx, source)
		if err != nil {
			return err
		}
		if seen {
			continue
		}
		if !j.settled(source, settledBefore) {
			continue
		}
		payload := importjob.Payload{
			SourcePath: source, Origin: library.ImportOriginWatched, CategoryID: categoryID,
			TransferLadder: folder.TransferLadder,
		}
		if err := j.config.Enqueue(ctx, payload); err != nil {
			return err
		}
		j.config.Logger.Info("importing from a download folder", slog.String("path", source))
	}
	return nil
}

// settled is whether nothing under source changed after settledBefore. An entry too large to
// check within maxSettleEntries is left for the next pass rather than imported unchecked.
func (j *Job) settled(source string, settledBefore time.Time) bool {
	pending := []string{source}
	for seen := 0; len(pending) > 0; seen++ {
		if seen > maxSettleEntries {
			return false
		}
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		info, err := j.config.FS.Lstat(current)
		if err != nil || info.ModTime.After(settledBefore) {
			return false
		}
		if !info.IsDir {
			continue
		}
		children, err := j.config.FS.ReadDir(current)
		if err != nil {
			return false
		}
		for _, child := range children {
			pending = append(pending, path.Join(current, child.Name))
		}
	}
	return true
}
