package tv

import (
	"context"
	"log/slog"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/works"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// RefreshJobType is the registered type of the series refresh sweep.
const RefreshJobType = jobs.Type("tv.refresh_series")

// RefreshActionKey deduplicates the sweep: one at a time (Invariant 16).
const RefreshActionKey = "tv.refresh_series"

// RefreshDefinition declares the sweep: background metadata work, retried on transient failure.
var RefreshDefinition = jobs.Definition{
	Name: "Refreshing series",
	Type: RefreshJobType, Pool: jobs.PoolMetadata, Mutating: true, MaxAttempts: 3,
}

const (
	// continuingRefresh and endedRefresh are Sonarr's pattern: a running series twice a day, an ended one weekly.
	continuingRefresh = 12 * time.Hour
	endedRefresh      = 7 * 24 * time.Hour
	// refreshPerRun bounds one sweep so a large library is spread over the hour's runs, not asked of the providers at once.
	refreshPerRun = 40
)

// RefreshConfig is what a RefreshJob needs; Logger is optional.
type RefreshConfig struct {
	Reader           library.SeriesReader
	Service          Service
	DefaultProfileID int64
	Categories       interface {
		Category(id int64) (settings.Category, bool)
		ProfileFor(category settings.Category, fallback int64) int64
	}
	Clock  clock.Clock
	Logger *slog.Logger
}

// NewRefreshJob is the shared refresh sweep over the series that are due, so new episodes, seasons, air times,
// credits and artwork reach the library without anyone pressing Refresh.
func NewRefreshJob(config RefreshConfig) works.RefreshJob {
	return works.RefreshJob{
		Name: MediaType, JobType: RefreshJobType, NoSource: ErrNoSource, Clock: config.Clock, Logger: config.Logger,
		Due: func(ctx context.Context) ([]library.RefreshCandidate, error) {
			return config.Reader.DueForRefresh(ctx, config.Clock.Now(), continuingRefresh, endedRefresh, refreshPerRun)
		},
		Warm: config.Service.WarmWikidata,
		Refresh: func(ctx context.Context, candidate library.RefreshCandidate) error {
			profile := config.DefaultProfileID
			if category, found := config.Categories.Category(candidate.CategoryID); found {
				profile = config.Categories.ProfileFor(category, profile)
			}
			_, err := config.Service.Refresh(ctx, RefreshRequest{WorkID: candidate.WorkID, CategoryID: candidate.CategoryID, ProfileID: profile})
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
