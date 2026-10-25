package movie

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
	refreshPerRun = 60
)

// RefreshConfig is what the refresh sweep needs; Logger is optional.
type RefreshConfig struct {
	Reader  library.SeriesReader
	Service Service
	Clock   clock.Clock
	Logger  *slog.Logger
}

// NewRefreshJob is the shared refresh sweep over the movies that are due, so new release dates, cuts and artwork
// reach the library on their own.
func NewRefreshJob(config RefreshConfig) works.RefreshJob {
	return works.RefreshJob{
		Name: MediaType, JobType: RefreshJobType, NoSource: ErrNoSource, Clock: config.Clock, Logger: config.Logger,
		Due: func(ctx context.Context) ([]library.RefreshCandidate, error) {
			return config.Reader.DueForRefreshOf(ctx, MediaType, string(StatusReleased), config.Clock.Now(), activeRefresh, releasedRefresh, refreshPerRun)
		},
		Warm: config.Service.WarmWikidata,
		Refresh: func(ctx context.Context, candidate library.RefreshCandidate) error {
			_, err := config.Service.Refresh(ctx, RefreshRequest{WorkID: candidate.WorkID, CategoryID: candidate.CategoryID})
			return err
		},
		MarkAttempt: func(ctx context.Context, workID int64) error {
			return config.Service.Series.MarkRefreshAttempt(ctx, config.Clock.Now(), workID)
		},
	}
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

// NewArtworkJob keeps movie libraries' posters, studio logos and billed-first cast photos in the picture cache.
func NewArtworkJob(reader picturesweep.Reader, cache picturesweep.Cache, logger *slog.Logger) *picturesweep.Job {
	return picturesweep.New(picturesweep.Config{MediaType: MediaType, OrganisationKind: "studio", Reader: reader, Cache: cache, Logger: logger})
}

// EnqueueArtwork submits one sweep, deduplicated by ArtworkActionKey.
func EnqueueArtwork(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{Type: ArtworkJobType, ActionKey: ArtworkActionKey, Priority: jobs.PriorityLow})
}
