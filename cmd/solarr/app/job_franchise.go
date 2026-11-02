package app

import (
	"context"
	"log/slog"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/service/franchisewatch"
)

// cacheLibraries lists the libraries the settings cache holds.
type cacheLibraries struct{ cache *settings.Cache }

func (l cacheLibraries) Categories() []settings.Category { return l.cache.Snapshot().Categories() }

// registerFranchiseExpandJob wires the sweep that adds what a monitored franchise gains later.
func (p *process) registerFranchiseExpandJob(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(franchisewatch.Definition); err != nil {
		return err
	}
	executor.Handle(franchisewatch.JobType, franchisewatch.Service{
		Libraries: cacheLibraries{cache: p.cache}, Franchises: library.NewFranchises(p.library), Media: p.media, Logger: p.logger,
	})
	return nil
}

// enqueueFranchiseExpand submits one sweep; registered as a task, so it also runs on its own schedule.
func (p *process) enqueueFranchiseExpand(ctx context.Context) (jobs.EnqueueResult, error) {
	result, err := franchisewatch.Enqueue(ctx, p.queue)
	if err != nil {
		p.logger.Warn("enqueue franchise sweep", slog.Any("error", err))
	}
	return result, err
}
