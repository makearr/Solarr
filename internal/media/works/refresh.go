package works

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/parallel"
)

// refreshWorkers is how many works are refreshed at once; four took a 1,062-series import from 15 s a series to about
// an hour in all (2026-10-03). Each provider's own bucket still paces its calls.
const refreshWorkers = 4

// RefreshJob re-reads the works of a type that are due, so new dates, parts, credits and artwork reach the library
// without anyone pressing Refresh. Due, Refresh and MarkAttempt are required.
type RefreshJob struct {
	// Name prefixes the log ("tv"); JobType names the job in a retryable error.
	Name    string
	JobType jobs.Type
	// Due lists the works to read again now, at most one run's worth.
	Due func(ctx context.Context) ([]library.RefreshCandidate, error)
	// Warm reads in bulk ahead what each refresh then asks of a shared source (Wikidata); nil warms nothing.
	Warm func(ctx context.Context, due []library.RefreshCandidate)
	// Refresh reads one work again.
	Refresh func(ctx context.Context, candidate library.RefreshCandidate) error
	// MarkAttempt records a refresh that failed, so the work waits its turn again rather than being asked every run.
	MarkAttempt func(ctx context.Context, workID int64) error
	// NoSource is the type's error for a work no provider answered, logged as such.
	NoSource error
	Clock    clock.Clock
	Logger   *slog.Logger
}

// Run refreshes the due works, refreshWorkers at a time; one failing never stops the rest.
func (j RefreshJob) Run(ctx context.Context, _ jobs.Job) error {
	logger := j.Logger
	if logger == nil {
		logger = slog.Default()
	}
	due, err := j.Due(ctx)
	if err != nil {
		return fmt.Errorf("%s: refresh sweep: %w", j.Name, err)
	}
	if j.Warm != nil {
		j.Warm(ctx, due)
	}
	outcomes := parallel.Do(ctx, len(due), refreshWorkers, func(ctx context.Context, index int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := j.Refresh(ctx, due[index])
		if err != nil {
			// Best effort: a work whose attempt is not recorded is only asked again sooner.
			_ = j.MarkAttempt(ctx, due[index].WorkID)
		}
		return err
	})
	if err := ctx.Err(); err != nil {
		return jobs.Retryablef(j.Name+".refresh.context", string(j.JobType), err)
	}
	refreshed, failed := 0, 0
	for index, err := range outcomes {
		switch {
		case err == nil:
			refreshed++
		case j.NoSource != nil && errors.Is(err, j.NoSource):
			failed++
			logger.WarnContext(ctx, j.Name+" refresh: no provider answered", slog.Int64("work_id", due[index].WorkID))
		default:
			failed++
			logger.WarnContext(ctx, j.Name+" refresh failed", slog.Int64("work_id", due[index].WorkID), slog.Any("error", err))
		}
	}
	logger.Info(j.Name+" refresh sweep finished", slog.Int("refreshed", refreshed), slog.Int("failed", failed))
	return nil
}
