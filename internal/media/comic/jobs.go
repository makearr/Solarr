package comic

import (
	"context"
	"log/slog"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/picturesweep"
	"github.com/makearr/Solarr/internal/media/works"
	"github.com/makearr/Solarr/internal/platform/clock"
)

const (
	// activeRefresh and settledRefresh: a running series daily, an ended one monthly.
	activeRefresh  = 24 * time.Hour
	settledRefresh = 30 * 24 * time.Hour
	// refreshPerRun bounds one sweep so ComicVine's 200 requests an hour per resource are never exceeded: a series of
	// many issues costs several requests, and the rest waits for the next run.
	refreshPerRun = 20
)

// RefreshJobType is the registered type of the comic refresh sweep.
const RefreshJobType jobs.Type = "comic.refresh_series"

// ArtworkJobType is the registered type of the sweep that keeps covers and publisher logos at hand.
const ArtworkJobType jobs.Type = "comic.artwork"

// RefreshDefinition declares the sweep: background metadata work, retried on transient failure.
func RefreshDefinition() jobs.Definition {
	return works.RefreshDefinition("Refreshing comic series", RefreshJobType)
}

// ArtworkDefinition declares the picture sweep.
func ArtworkDefinition() jobs.Definition {
	return works.ArtworkDefinition("Fetching comic covers", ArtworkJobType)
}

// EnqueueRefresh submits one refresh sweep, one at a time (Invariant 16).
func EnqueueRefresh(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return works.EnqueueRefresh(ctx, queue, RefreshJobType)
}

// EnqueueArtwork submits one picture sweep.
func EnqueueArtwork(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return works.EnqueueArtwork(ctx, queue, ArtworkJobType)
}

// NewRefreshJob is the shared refresh sweep over the series that are due.
func NewRefreshJob(reader library.SeriesReader, service Service, clock clock.Clock, logger *slog.Logger) works.RefreshJob {
	return works.RefreshJob{
		Name: MediaType, JobType: RefreshJobType, NoSource: ErrNoSource, Clock: clock, Logger: logger,
		Due: func(ctx context.Context) ([]library.RefreshCandidate, error) {
			return reader.DueForRefreshOf(ctx, MediaType, "ended", clock.Now(), activeRefresh, settledRefresh, refreshPerRun)
		},
		Refresh: func(ctx context.Context, candidate library.RefreshCandidate) error {
			_, err := service.Refresh(ctx, candidate.CategoryID, candidate.WorkID)
			return err
		},
		MarkAttempt: func(ctx context.Context, workID int64) error {
			return service.Series.MarkRefreshAttempt(ctx, clock.Now(), workID)
		},
	}
}

// NewArtworkJob keeps covers and publisher logos in the picture cache.
func NewArtworkJob(reader picturesweep.Reader, cache picturesweep.Cache, logger *slog.Logger) *picturesweep.Job {
	return picturesweep.New(picturesweep.Config{MediaType: MediaType, OrganisationKind: "publisher", Reader: reader, Cache: cache, Logger: logger})
}
