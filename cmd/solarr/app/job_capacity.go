package app

import (
	"context"
	"log/slog"

	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/jobs/capacity"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// registerCapacityJob wires section 11.7's capacity-polling sweep into the
// registry and the executor.
func (p *process) registerCapacityJob(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(capacity.Definition); err != nil {
		return err
	}
	job := capacity.NewJob(capacity.Config{
		FS: p.fsys, Cache: p.cache, Clock: clock.NewSystem(), Logger: p.logger,
	})
	executor.Handle(capacity.JobType, job)
	return nil
}

// enqueueCapacity submits one capacity sweep. Registered as a task with
// internal/jobs.TaskRunner (todo 4.19), so it also fires on its own
// schedule, not only once at startup.
func (p *process) enqueueCapacity(ctx context.Context) (jobs.EnqueueResult, error) {
	result, err := capacity.Enqueue(ctx, p.queue)
	if err != nil {
		p.logger.Warn("enqueue capacity sweep", slog.Any("error", err))
	}
	return result, err
}
