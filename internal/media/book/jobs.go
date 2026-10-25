package book

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/picturesweep"
	"github.com/makearr/Solarr/internal/moving/renamejob"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/parallel"
)

const (
	// activeRefresh and settledRefresh: a book still to come weekly, a published one every three months, as its facts
	// rarely change once it is out.
	activeRefresh  = 7 * 24 * time.Hour
	settledRefresh = 90 * 24 * time.Hour
	// refreshPerRun bounds one sweep so a large library is spread over the day's runs.
	refreshPerRun  = 60
	refreshWorkers = 4
)

// RefreshJobType is the registered type of the format's refresh sweep.
func (f Format) RefreshJobType() jobs.Type { return jobs.Type(f.MediaType + ".refresh_books") }

// RefreshDefinition declares the format's sweep: background metadata work, retried on transient failure.
func (f Format) RefreshDefinition() jobs.Definition {
	return jobs.Definition{Name: "Refreshing " + lower(f.Many), Type: f.RefreshJobType(), Pool: jobs.PoolMetadata, Mutating: true, MaxAttempts: 3}
}

// EnqueueRefresh submits one refresh sweep, one at a time (Invariant 16).
func (f Format) EnqueueRefresh(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{Type: f.RefreshJobType(), ActionKey: string(f.RefreshJobType()), Priority: jobs.PriorityLow, Payload: []byte("{}")})
}

// RefreshJob re-reads the books that are due, so new covers, descriptions and series places reach the library.
type RefreshJob struct {
	Reader  library.SeriesReader
	Service Service
	Clock   clock.Clock
	Logger  *slog.Logger
}

// Run refreshes the due books, refreshWorkers at a time; one failing never stops the rest.
func (j RefreshJob) Run(ctx context.Context, _ jobs.Job) error {
	format := j.Service.Format
	logger := j.Logger
	if logger == nil {
		logger = slog.Default()
	}
	due, err := j.Reader.DueForRefreshOf(ctx, format.MediaType, string(StatusPublished), j.Clock.Now(), activeRefresh, settledRefresh, refreshPerRun)
	if err != nil {
		return fmt.Errorf("book: refresh sweep: %w", err)
	}
	j.Service.WarmWikidata(ctx, due)
	outcomes := parallel.Do(ctx, len(due), refreshWorkers, func(ctx context.Context, index int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, err := j.Service.Refresh(ctx, due[index].CategoryID, due[index].WorkID)
		if err != nil {
			_ = j.Service.Series.MarkRefreshAttempt(ctx, j.Clock.Now(), due[index].WorkID)
		}
		return err
	})
	if err := ctx.Err(); err != nil {
		return jobs.Retryablef("book.refresh.context", string(format.RefreshJobType()), err)
	}
	refreshed, failed := 0, 0
	for index, err := range outcomes {
		switch {
		case err == nil:
			refreshed++
		case errors.Is(err, ErrNoSource):
			failed++
			logger.WarnContext(ctx, "book refresh: no provider answered", slog.Int64("work_id", due[index].WorkID))
		default:
			failed++
			logger.WarnContext(ctx, "book refresh failed", slog.Int64("work_id", due[index].WorkID), slog.Any("error", err))
		}
	}
	logger.Info("book refresh sweep finished", slog.String("media_type", format.MediaType), slog.Int("refreshed", refreshed), slog.Int("failed", failed))
	return nil
}

// ArtworkJobType is the registered type of the sweep that keeps a library's covers and author photos at hand.
func (f Format) ArtworkJobType() jobs.Type { return jobs.Type(f.MediaType + ".artwork") }

// ArtworkDefinition declares the format's picture sweep.
func (f Format) ArtworkDefinition() jobs.Definition {
	return jobs.Definition{Name: "Fetching " + lower(f.One) + " covers", Type: f.ArtworkJobType(), Pool: jobs.PoolMetadata, Mutating: true, MaxAttempts: 1}
}

// NewArtworkJob keeps the format's covers, publisher logos and author photos in the picture cache.
func (f Format) NewArtworkJob(reader picturesweep.Reader, cache picturesweep.Cache, logger *slog.Logger) *picturesweep.Job {
	return picturesweep.New(picturesweep.Config{MediaType: f.MediaType, OrganisationKind: "publisher", Reader: reader, Cache: cache, Logger: logger})
}

// EnqueueArtwork submits one picture sweep.
func (f Format) EnqueueArtwork(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{Type: f.ArtworkJobType(), ActionKey: string(f.ArtworkJobType()), Priority: jobs.PriorityLow})
}

// RenameConfig is what naming a library's book files needs (Readarr's Rename Books); renamejob.Engine does the rest.
type RenameConfig struct {
	Format Format
	Reader library.SeriesReader
	Clock  clock.Clock
}

// NameFiles is the format's part of a rename: an ebook's files each the whole book, an audiobook's numbered as its
// parts in the order their names sort.
func (cfg RenameConfig) NameFiles(ctx context.Context, category settings.Category, workID int64, files []renamejob.File) ([]renamejob.Named, error) {
	facts, err := NamingFacts(ctx, cfg.Reader, cfg.Format, category.ID, workID, cfg.Clock.Now())
	if err != nil {
		return nil, err
	}
	names := cfg.Format.NamingFor(category.Naming)
	ordered := slices.Clone(files)
	slices.SortStableFunc(ordered, func(a, b renamejob.File) int { return NaturalCompare(a.RelPath, b.RelPath) })
	named := make([]renamejob.Named, 0, len(ordered))
	taken := map[string]bool{}
	for index, file := range ordered {
		facts := facts
		if cfg.Format.ByFolder {
			facts.PartNumber, facts.PartCount = index+1, len(ordered)
		}
		rendered, warnings, err := names.RenderBook(facts, file.RelPath)
		if err != nil || taken[rendered] {
			continue
		}
		taken[rendered] = true
		named = append(named, renamejob.Named{MediaObjectID: file.MediaObjectID, RelPath: rendered, Warnings: warnings})
	}
	return named, nil
}
