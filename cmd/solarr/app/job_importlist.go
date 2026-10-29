package app

import (
	"context"
	"log/slog"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/service/importlist"
)

// importListService is import lists for every media type: the API's preview and check, and the sync.
func (p *process) importListService() importlist.Service {
	p.tvService()
	wall := clock.NewSystem()
	return importlist.Service{
		Lists: p.cache, Records: library.NewImportLists(p.library), Exclusions: library.NewExclusions(p.library),
		Works: library.NewSeries(p.library), Libraries: adapters.Categories{Cache: p.cache}, Media: p.media,
		Catalogue: importlist.NewCatalogue(p.anilistShared, p.tmdbShared, func() importlist.Season { return importlist.SeasonOf(wall.Now()) }),
		Clock:     wall, Logger: p.logger,
		AfterAdd: func(ctx context.Context) { _, _ = p.enqueueCategoryCounts(ctx) },
	}
}

// registerImportListJob wires the sync that adds what enabled import lists name.
func (p *process) registerImportListJob(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(importlist.Definition); err != nil {
		return err
	}
	executor.Handle(importlist.JobType, p.importListService())
	return nil
}

// enqueueImportListSync submits the sweep of the lists that are due; registered as a task, so it runs hourly.
func (p *process) enqueueImportListSync(ctx context.Context) (jobs.EnqueueResult, error) {
	result, err := importlist.Enqueue(ctx, p.queue, 0)
	if err != nil {
		p.logger.Warn("enqueue import list sync", slog.Any("error", err))
	}
	return result, err
}
