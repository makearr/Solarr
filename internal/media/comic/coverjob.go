package comic

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path"
	"slices"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/archive"
	"github.com/makearr/Solarr/internal/platform/artwork"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// CoverJobType is the registered type of the sweep that takes a series' cover from the first page of its first issue.
const CoverJobType = jobs.Type("comic.first_page_cover")

// CoverActionKey keeps one such sweep active at a time.
const CoverActionKey = "comic.first_page_cover"

// CoverDefinition is background picture work in the metadata pool, as the other picture sweeps are.
var CoverDefinition = jobs.Definition{
	Name: "Taking comic covers from the files",
	Type: CoverJobType, Pool: jobs.PoolMetadata, Mutating: true, MaxAttempts: 3,
}

const (
	coverBatch      = 20
	coverRetryAfter = 7 * 24 * time.Hour
	// coverSource is the miss key: a series with no readable first page is left alone for coverRetryAfter.
	coverSource = "first-page"
	// maxCoverPage bounds the page read into memory.
	maxCoverPage = 40 << 20
	// maxCoverEntries bounds how far into an archive the page list is read.
	maxCoverEntries = 5000
)

var errNoCoverPage = errors.New("the issue holds no page image")

// CoverRoots resolves a root folder by id.
type CoverRoots interface {
	RootFolder(id int64) (settings.RootFolder, bool)
}

// CoverPictures is what the job asks of the library: works lacking a cover, and whether a provider already has a poster.
type CoverPictures interface {
	Pictures(ctx context.Context, workID int64) (map[string]string, error)
}

// CoverJobConfig is what a CoverJob needs.
type CoverJobConfig struct {
	Artworks  library.Artworks
	Pictures  CoverPictures
	Roots     CoverRoots
	FS        fsx.FS
	ImagesDir string
	Clock     clock.Clock
	Logger    *slog.Logger
}

// CoverJob gives a series without a cover of its own the first page of its first issue, so a series only a catalogue
// without pictures knows still shows something. A provider's poster, when there is one, is left to win.
type CoverJob struct{ config CoverJobConfig }

// NewCoverJob builds the handler.
func NewCoverJob(config CoverJobConfig) *CoverJob {
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	return &CoverJob{config: config}
}

// EnqueueCovers submits one sweep, deduplicated.
func EnqueueCovers(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{Type: CoverJobType, ActionKey: CoverActionKey, Priority: jobs.PriorityLow, Payload: []byte("{}")})
}

// Run walks the series without a cover by work id.
func (j *CoverJob) Run(ctx context.Context, job jobs.Job) error {
	ask := library.ArtworkAsk{Source: coverSource, MissedSince: j.config.Clock.Now().Add(-coverRetryAfter).Unix()}
	var after int64
	done, missed, failed := 0, 0, 0
	for {
		if err := ctx.Err(); err != nil {
			return jobs.Retryablef("comic.cover.context", string(CoverJobType), err)
		}
		batch, err := j.config.Artworks.NeedingEmbeddedArtwork(ctx, MediaType, ask, after, coverBatch)
		if err != nil {
			return fmt.Errorf("comic: covers: %w", err)
		}
		if len(batch) == 0 {
			break
		}
		var misses []int64
		for _, candidate := range batch {
			after = candidate.WorkID
			switch err := j.one(ctx, candidate); {
			case err == nil:
				done++
			case errors.Is(err, errPosterWins):
			case errors.Is(err, errNoCoverPage), errors.Is(err, errCoverRoot), errors.Is(err, errNotAnImage):
				missed++
				misses = append(misses, candidate.WorkID)
			default:
				failed++
				j.config.Logger.WarnContext(ctx, "comic cover from the first page failed", slog.Int64("work_id", candidate.WorkID), slog.Any("error", err))
			}
		}
		if err := j.config.Artworks.RecordMisses(ctx, coverSource, misses, j.config.Clock.Now().Unix()); err != nil {
			return fmt.Errorf("comic: covers: %w", err)
		}
		job.Progress.Add(int64(len(batch)))
		if len(batch) < coverBatch {
			break
		}
	}
	j.config.Logger.Info("comic cover sweep finished", slog.Int("covered", done), slog.Int("no_page", missed), slog.Int("failed", failed))
	return nil
}

var (
	errPosterWins = errors.New("a provider poster covers the series")
	errCoverRoot  = errors.New("root folder is not configured")
	errNotAnImage = errors.New("not a usable image")
)

func (j *CoverJob) one(ctx context.Context, candidate library.EmbeddedArtworkCandidate) error {
	if pictures, err := j.config.Pictures.Pictures(ctx, candidate.WorkID); err == nil && pictures["poster"] != "" {
		return errPosterWins
	}
	root, found := j.config.Roots.RootFolder(candidate.RootFolderID)
	if !found {
		return errCoverRoot
	}
	page, err := firstPage(ctx, j.config.FS, path.Join(root.Path, candidate.RelPath))
	if err != nil {
		return err
	}
	cacheKey := fmt.Sprintf("work-%d-cover", candidate.WorkID)
	sizes, err := artwork.Process(ctx, page)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return fmt.Errorf("process page: %w: %w", errNotAnImage, err)
	}
	if err := j.config.FS.MkdirAll(j.config.ImagesDir, 0o755); err != nil {
		return err
	}
	for size, image := range sizes {
		destination, err := artwork.ShardedFor(j.config.FS, j.config.ImagesDir, cacheKey+"-"+coverSizeName(size)+".webp")
		if err != nil {
			return err
		}
		if err := fsx.WriteAtomic(j.config.FS, destination, func(w io.Writer) error {
			_, err := w.Write(image.Bytes)
			return err
		}); err != nil {
			return err
		}
	}
	full := sizes[artwork.Full]
	return j.config.Artworks.Record(ctx, candidate.WorkID, "", cacheKey, full.Width, full.Height, j.config.Clock.Now().Unix())
}

func coverSizeName(size artwork.Size) string {
	switch size {
	case artwork.Thumb:
		return "thumb"
	case artwork.Small:
		return "small"
	default:
		return "full"
	}
}

// firstPage reads the first page image of an issue file (cbz, cbr, cb7, cbt): the page named cover, else the first in reading order.
func firstPage(ctx context.Context, fsys fsx.FS, issue string) ([]byte, error) {
	listed, _, err := archive.Peek(ctx, fsys, issue, func(archive.Entry) int64 { return 0 }, maxCoverEntries)
	if err != nil {
		return nil, err
	}
	var names []string
	sizes := map[string]int64{}
	for _, entry := range listed {
		if !entry.Entry.IsDir && isPage(path.Base(entry.Entry.Name)) && !entry.Entry.Encrypted {
			names = append(names, entry.Entry.Name)
			sizes[entry.Entry.Name] = entry.Entry.SizeBytes
		}
	}
	if len(names) == 0 {
		return nil, errNoCoverPage
	}
	slices.SortStableFunc(names, func(a, b string) int { return pageOrder(path.Base(a), path.Base(b)) })
	first := names[0]
	if sizes[first] > maxCoverPage {
		return nil, errNotAnImage
	}
	read, _, err := archive.Peek(ctx, fsys, issue, func(entry archive.Entry) int64 {
		if entry.Name == first {
			return entry.SizeBytes
		}
		return 0
	}, maxCoverEntries)
	if err != nil {
		return nil, err
	}
	for _, entry := range read {
		if entry.Entry.Name == first && len(entry.Head) > 0 {
			return entry.Head, nil
		}
	}
	return nil, errNoCoverPage
}
