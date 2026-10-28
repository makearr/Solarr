package book

import (
	"context"
	"log/slog"
	"slices"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/picturesweep"
	"github.com/makearr/Solarr/internal/media/works"
	"github.com/makearr/Solarr/internal/moving/renamejob"
	"github.com/makearr/Solarr/internal/platform/clock"
)

const (
	// activeRefresh and settledRefresh: a book still to come weekly, a published one every three months, as its facts
	// rarely change once it is out.
	activeRefresh  = 7 * 24 * time.Hour
	settledRefresh = 90 * 24 * time.Hour
	// refreshPerRun bounds one sweep so a large library is spread over the day's runs.
	refreshPerRun = 60
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

// NewRefreshJob is the shared refresh sweep over the format's books that are due, so new covers, descriptions and
// series places reach the library.
func (f Format) NewRefreshJob(reader library.SeriesReader, service Service, clock clock.Clock, logger *slog.Logger) works.RefreshJob {
	return works.RefreshJob{
		Name: f.MediaType, JobType: f.RefreshJobType(), NoSource: ErrNoSource, Clock: clock, Logger: logger,
		Due: func(ctx context.Context) ([]library.RefreshCandidate, error) {
			if merged, err := service.Franchises.MergeSameNamed(ctx, f.MediaType, ProviderSeries); err != nil {
				logger.Warn("book series not merged", slog.String("media_type", f.MediaType), slog.Any("error", err))
			} else if merged > 0 {
				logger.Info("book series merged", slog.String("media_type", f.MediaType), slog.Int("merged", merged))
			}
			return reader.DueForRefreshOf(ctx, f.MediaType, string(StatusPublished), clock.Now(), activeRefresh, settledRefresh, refreshPerRun)
		},
		Warm: service.WarmWikidata,
		Refresh: func(ctx context.Context, candidate library.RefreshCandidate) error {
			_, err := service.Refresh(ctx, candidate.CategoryID, candidate.WorkID)
			return err
		},
		MarkAttempt: func(ctx context.Context, workID int64) error {
			return service.Series.MarkRefreshAttempt(ctx, clock.Now(), workID)
		},
	}
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
