package main

import (
	"context"
	"log/slog"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/jobs"
	"github.com/makearr/MediaNexus/internal/library"
	"github.com/makearr/MediaNexus/internal/mediatool"
	"github.com/makearr/MediaNexus/internal/probe"
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
		Roots:    cacheRoots{cache: p.cache},
		Tool:     mediatool.New(nil, 0),
		Clock:    clock.NewSystem(),
		Logger:   p.logger,
	})
	executor.Handle(probe.JobType, job)
	return nil
}

// enqueueProbe submits one probe sweep, once at startup -- there is no
// scheduler yet, the same gap enqueueLibraryScans and enqueueEnrichment
// already carry, so a file a scan registers while this process is already
// running waits for the next restart to be measured.
func (p *process) enqueueProbe(ctx context.Context) {
	if _, err := probe.Enqueue(ctx, p.queue); err != nil {
		p.logger.Warn("enqueue probe sweep", slog.Any("error", err))
	}
}
