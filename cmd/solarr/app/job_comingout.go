package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
)

// comingOutJobType is the registered type of the re-count of works whose parts came out.
const comingOutJobType = jobs.Type("library.recount_coming_out")

var comingOutDefinition = jobs.Definition{
	Name: "Counting what has come out",
	Type: comingOutJobType, Pool: jobs.PoolHousekeeping, Mutating: true, MaxAttempts: 3,
}

// comingOutJob makes an episode that has aired count as missing: nothing is written when time passes, so a season
// would stay as complete as it was before its next episode came out (section 19.2).
type comingOutJob struct {
	desired library.Desired
	series  library.Series
	grace   func() library.AvailabilityGrace
	logger  *slog.Logger
}

// Run stamps the settings' wait onto every part first, so a changed setting counts from the next run.
func (j comingOutJob) Run(ctx context.Context, _ jobs.Job) error {
	grace := j.grace()
	if stamped, err := j.series.StampGrace(ctx, grace); err != nil {
		return err
	} else if stamped > 0 {
		j.logger.Info("the wait after an air time was stamped on parts", slog.Int("parts", stamped))
	}
	changed, err := j.desired.RecountComingOut(ctx, grace.Longest())
	if err != nil {
		return err
	}
	if changed > 0 {
		j.logger.Info("parts came out: their works were counted again", slog.Int("items", changed))
	}
	return nil
}

// registerComingOutJob wires the re-count into the registry and the executor.
func (p *process) registerComingOutJob(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(comingOutDefinition); err != nil {
		return err
	}
	executor.Handle(comingOutJobType, comingOutJob{
		desired: library.NewDesired(p.library), series: library.NewSeries(p.library), grace: p.availabilityGrace, logger: p.logger,
	})
	return nil
}

// enqueueComingOut submits one re-count; a second while one waits is the same work.
func (p *process) enqueueComingOut(ctx context.Context) (jobs.EnqueueResult, error) {
	result, err := p.queue.Enqueue(ctx, jobs.Request{
		Type: comingOutJobType, ActionKey: string(comingOutJobType), Priority: jobs.PriorityLow, Payload: []byte("{}"),
	})
	if err != nil {
		p.logger.Warn("enqueue coming-out re-count", slog.Any("error", err))
	}
	return result, err
}

// availabilityGrace is the settings' wait after a part's time, as the library counts it.
func (p *process) availabilityGrace() library.AvailabilityGrace {
	hours := p.cache.Snapshot().AvailabilityGrace()
	return library.AvailabilityGrace{
		High: time.Duration(hours.High) * time.Hour, Medium: time.Duration(hours.Medium) * time.Hour, Low: time.Duration(hours.Low) * time.Hour,
	}
}
