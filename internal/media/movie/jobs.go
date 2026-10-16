package movie

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/parallel"
)

// RefreshJobType is the registered type of the movie refresh sweep.
const RefreshJobType = jobs.Type("movie.refresh_movies")

// RefreshActionKey deduplicates the sweep: one at a time (Invariant 16).
const RefreshActionKey = "movie.refresh_movies"

// RefreshDefinition declares the sweep: background metadata work, retried on transient failure.
var RefreshDefinition = jobs.Definition{
	Name: "Refreshing movies",
	Type: RefreshJobType, Pool: jobs.PoolMetadata, Mutating: true, MaxAttempts: 3,
}

const (
	// activeRefresh and releasedRefresh: a movie still to come (new dates, a new cut, a cast) weekly, a released one monthly.
	activeRefresh   = 7 * 24 * time.Hour
	releasedRefresh = 30 * 24 * time.Hour
	// refreshPerRun bounds one sweep so a large library is spread over the hour's runs.
	refreshPerRun  = 60
	refreshWorkers = 4
)

// RefreshConfig is what a RefreshJob needs; Logger is optional.
type RefreshConfig struct {
	Reader  library.SeriesReader
	Service Service
	Clock   clock.Clock
	Logger  *slog.Logger
}

// RefreshJob re-reads the movies that are due, so new release dates, cuts and artwork reach the library on their own.
type RefreshJob struct {
	config RefreshConfig
	logger *slog.Logger
}

// NewRefreshJob builds the handler.
func NewRefreshJob(config RefreshConfig) *RefreshJob {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &RefreshJob{config: config, logger: logger}
}

// Run refreshes the due movies, refreshWorkers at a time; one failing never stops the rest.
func (j *RefreshJob) Run(ctx context.Context, _ jobs.Job) error {
	due, err := j.config.Reader.DueForRefreshOf(ctx, MediaType, string(StatusReleased), j.config.Clock.Now(), activeRefresh, releasedRefresh, refreshPerRun)
	if err != nil {
		return fmt.Errorf("movie: refresh sweep: %w", err)
	}
	outcomes := parallel.Do(ctx, len(due), refreshWorkers, func(ctx context.Context, index int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		candidate := due[index]
		_, err := j.config.Service.Refresh(ctx, RefreshRequest{WorkID: candidate.WorkID, CategoryID: candidate.CategoryID})
		if err != nil {
			_ = j.config.Service.Series.MarkRefreshAttempt(ctx, j.config.Clock.Now(), candidate.WorkID)
		}
		return err
	})
	if err := ctx.Err(); err != nil {
		return jobs.Retryablef("movie.refresh.context", string(RefreshJobType), err)
	}
	refreshed, failed := 0, 0
	for index, err := range outcomes {
		switch {
		case err == nil:
			refreshed++
		case errors.Is(err, ErrNoSource):
			failed++
			j.logger.WarnContext(ctx, "movie refresh: no provider answered", slog.Int64("work_id", due[index].WorkID))
		default:
			failed++
			j.logger.WarnContext(ctx, "movie refresh failed", slog.Int64("work_id", due[index].WorkID), slog.Any("error", err))
		}
	}
	j.logger.Info("movie refresh sweep finished", slog.Int("refreshed", refreshed), slog.Int("failed", failed))
	return nil
}

// EnqueueRefresh submits one refresh sweep, deduplicated by RefreshActionKey.
func EnqueueRefresh(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{Type: RefreshJobType, ActionKey: RefreshActionKey, Priority: jobs.PriorityLow, Payload: []byte("{}")})
}

// ArtworkJobType is the registered type of the sweep that keeps a movie library's pictures at hand.
const ArtworkJobType = jobs.Type("movie.artwork")

// ArtworkActionKey deduplicates the sweep: one at a time (Invariant 16).
const ArtworkActionKey = "movie.artwork"

// ArtworkDefinition declares the sweep: background metadata work, resumed by the next run.
var ArtworkDefinition = jobs.Definition{
	Name: "Fetching movie pictures",
	Type: ArtworkJobType, Pool: jobs.PoolMetadata, Mutating: true, MaxAttempts: 1,
}

const (
	artworkPerRun  = 1500
	artworkWorkers = 6
	maxPhotos      = 6000
)

// PictureCache is the provider picture cache as the sweep needs it.
type PictureCache interface {
	Cached(address string) bool
	Ensure(ctx context.Context, address string) (string, error)
}

// ArtworkConfig is what an ArtworkJob needs; Logger is optional.
type ArtworkConfig struct {
	Reader library.SeriesReader
	Cache  PictureCache
	Logger *slog.Logger
}

// ArtworkJob fetches the posters, studio logos and first-billed photos a movie library shows before anyone scrolls to
// them, so a poster fetched when it is first seen does not cost the viewer the CDN's latency.
type ArtworkJob struct {
	config ArtworkConfig
	logger *slog.Logger
}

// NewArtworkJob builds the handler.
func NewArtworkJob(config ArtworkConfig) *ArtworkJob {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &ArtworkJob{config: config, logger: logger}
}

// Run fetches what is missing, posters first.
func (j *ArtworkJob) Run(ctx context.Context, _ jobs.Job) error {
	set, err := j.config.Reader.PictureAddressesOf(ctx, MediaType, "studio", maxPhotos)
	if err != nil {
		return err
	}
	var missing []string
	for _, group := range [][]string{set.Posters, set.Logos, set.Photos} {
		for _, address := range group {
			if !j.config.Cache.Cached(address) {
				missing = append(missing, address)
			}
		}
	}
	total := len(missing)
	if len(missing) > artworkPerRun {
		missing = missing[:artworkPerRun]
	}
	fetched, bad := 0, 0
	outcomes := parallel.Do(ctx, len(missing), artworkWorkers, func(ctx context.Context, index int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, err := j.config.Cache.Ensure(ctx, missing[index])
		return err
	})
	for _, err := range outcomes {
		switch {
		case err == nil:
			fetched++
		case ctx.Err() == nil:
			bad++
		}
	}
	j.logger.Info("movie pictures fetched", slog.Int("fetched", fetched), slog.Int("failed", bad), slog.Int("still_missing", total-fetched-bad))
	return ctx.Err()
}

// EnqueueArtwork submits one sweep, deduplicated by ArtworkActionKey.
func EnqueueArtwork(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{Type: ArtworkJobType, ActionKey: ArtworkActionKey, Priority: jobs.PriorityLow})
}
