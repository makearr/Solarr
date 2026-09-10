package app

import (
	"context"
	"log/slog"

	"github.com/makearr/MediaNexus/cmd/medianexus/adapters"
	"github.com/makearr/MediaNexus/internal/db/library"
	"github.com/makearr/MediaNexus/internal/jobs"
	"github.com/makearr/MediaNexus/internal/platform/clock"
	"github.com/makearr/MediaNexus/internal/platform/mediatool"
	"github.com/makearr/MediaNexus/internal/scanning/probe"
)

// registerProbeJob wires ADR-39's measured-half sweep into the registry and the
// executor: its own mediatool.Tool instance, separate from the scan's, since the
// two run independently and at different priorities (todo 4.2, confirmed with the
// user: probing is background work, never inside the scan).
func (p *process) registerProbeJob(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(probe.Definition); err != nil {
		return err
	}
	job := probe.NewJob(probe.Config{
		Physical: library.NewPhysical(p.library),
		Coverage: library.NewProbeCoverage(p.library, clock.NewSystem()),
		Roots:    adapters.Roots{Cache: p.cache},
		Tool:     mediatool.New(nil, 0),
		Clock:    clock.NewSystem(),
		Logger:   p.logger,
	})
	executor.Handle(probe.JobType, job)
	return nil
}

// enqueueProbe submits one probe sweep. Registered as a task with
// internal/jobs.TaskRunner (todo 4.19), so it also fires on its own
// schedule, not only once at startup.
func (p *process) enqueueProbe(ctx context.Context) (jobs.EnqueueResult, error) {
	result, err := probe.Enqueue(ctx, p.queue)
	if err != nil {
		p.logger.Warn("enqueue probe sweep", slog.Any("error", err))
	}
	return result, err
}
