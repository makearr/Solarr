package app

import (
	"context"
	"log/slog"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/importing/downloadwatch"
	"github.com/makearr/Solarr/internal/importing/importjob"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// registerDownloadWatchJob wires the download folder watch (2026-09-19 review).
func (p *process) registerDownloadWatchJob(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(downloadwatch.Definition); err != nil {
		return err
	}
	executor.Handle(downloadwatch.JobType, downloadwatch.NewJob(downloadwatch.Config{
		Folders: p.cache, FS: p.fsys, Sessions: library.NewImportSessions(p.library),
		Enqueue: func(ctx context.Context, payload importjob.Payload) error {
			_, err := importjob.Enqueue(ctx, p.queue, payload)
			return err
		},
		Clock: clock.NewSystem(), Settle: downloadwatch.DefaultSettle, Logger: p.logger,
	}))
	return nil
}

// enqueueDownloadWatch submits one pass over every watched folder.
func (p *process) enqueueDownloadWatch(ctx context.Context) (jobs.EnqueueResult, error) {
	result, err := downloadwatch.Enqueue(ctx, p.queue)
	if err != nil {
		p.logger.Warn("enqueue download folder watch", slog.Any("error", err))
	}
	return result, err
}
