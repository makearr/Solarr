package tv

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
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

// RefreshJob re-reads the series that are due, so new episodes, seasons, air times, credits and artwork reach the library
// without anyone pressing Refresh.
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

// Run refreshes the due series one by one; one failing never stops the rest.
func (j *RefreshJob) Run(ctx context.Context, _ jobs.Job) error {
	due, err := j.config.Reader.DueForRefresh(ctx, j.config.Clock.Now(), continuingRefresh, endedRefresh, refreshPerRun)
	if err != nil {
		return fmt.Errorf("tv: refresh sweep: %w", err)
	}
	refreshed, failed := 0, 0
	for _, candidate := range due {
		if err := ctx.Err(); err != nil {
			return jobs.Retryablef("tv.refresh.context", string(RefreshJobType), err)
		}
		profile := j.config.DefaultProfileID
		if category, found := j.config.Categories.Category(candidate.CategoryID); found {
			profile = j.config.Categories.ProfileFor(category, profile)
		}
		_, err := j.config.Service.Refresh(ctx, RefreshRequest{WorkID: candidate.WorkID, CategoryID: candidate.CategoryID, ProfileID: profile})
		if err != nil {
			_ = j.config.Service.Series.MarkRefreshAttempt(ctx, j.config.Clock.Now(), candidate.WorkID)
		}
		switch {
		case err == nil:
			refreshed++
		case errors.Is(err, ErrNoSource):
			failed++
			j.logger.WarnContext(ctx, "tv refresh: no provider answered", slog.Int64("work_id", candidate.WorkID))
		default:
			failed++
			j.logger.WarnContext(ctx, "tv refresh failed", slog.Int64("work_id", candidate.WorkID), slog.Any("error", err))
		}
	}
	j.logger.Info("tv refresh sweep finished", slog.Int("refreshed", refreshed), slog.Int("failed", failed))
	return nil
}

// EnqueueRefresh submits one refresh sweep, deduplicated by RefreshActionKey.
func EnqueueRefresh(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{Type: RefreshJobType, ActionKey: RefreshActionKey, Priority: jobs.PriorityLow, Payload: []byte("{}")})
}
