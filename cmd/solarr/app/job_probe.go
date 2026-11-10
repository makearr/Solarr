package app

import (
	"context"
	"log/slog"
	"path/filepath"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/mediatool"
	"github.com/makearr/Solarr/internal/scanning/probe"
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
		Physical:          library.NewPhysical(p.library),
		Coverage:          library.NewProbeCoverage(p.library, clock.NewSystem()),
		Roots:             adapters.Roots{Cache: p.cache},
		Tool:              mediatool.New(nil, 0),
		FS:                p.fsys,
		PerceptualHashing: func() bool { return p.cache.Snapshot().VideoPerceptualHash() },
		VideoPreviews:     func() bool { return p.cache.Snapshot().VideoPreviews() },
		PreviewsDir:       filepath.Join(p.config.Paths.ConfigDir, "previews"),
		Clock:             clock.NewSystem(),
		Logger:            p.logger,
		OthersWaiting: func(ctx context.Context) bool {
			waiting, err := p.queue.ClaimableIn(ctx, jobs.PoolFiles)
			return err != nil || waiting > 0
		},
	})
	p.probeJob = job
	executor.Handle(probe.JobType, job)
	if err := registry.Register(probe.PicturesDefinition); err != nil {
		return err
	}
	executor.Handle(probe.PicturesJobType, probe.NewPicturesJob(job, p.queue))
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

// videoPictures starts the on-demand preview and hash sweeps of Settings, General.
type videoPictures struct{ process *process }

func (v videoPictures) Generate(ctx context.Context, withPreviews bool) error {
	mode := probe.PicturesHashesOnly
	if withPreviews {
		mode = probe.PicturesWithPreviews
	}
	_, err := v.process.probeJob.RequestPictures(ctx, v.process.queue, mode)
	return err
}

// afterSweep catches counts up and measures newly registered files now rather than at the next 02:00 sweep.
func (p *process) afterSweep(ctx context.Context) {
	_, _ = p.enqueueCategoryCounts(ctx)
	_, _ = p.enqueueProbe(ctx)
}
