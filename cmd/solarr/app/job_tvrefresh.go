package app

import (
	"context"
	"log/slog"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// registerTVRefreshJob wires the series refresh sweep: new episodes, seasons, air times and credits without a click.
func (p *process) registerTVRefreshJob(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(tv.RefreshDefinition); err != nil {
		return err
	}
	executor.Handle(tv.RefreshJobType, tv.NewRefreshJob(tv.RefreshConfig{
		Reader: library.NewSeriesReader(p.library), Service: p.tvService(), DefaultProfileID: p.defaultQualityProfileID,
		Categories: adapters.Categories{Cache: p.cache}, Clock: clock.NewSystem(), Logger: p.logger,
	}))
	return nil
}

// enqueueTVRefresh submits one refresh sweep; registered as the "Refresh series" task.
func (p *process) enqueueTVRefresh(ctx context.Context) (jobs.EnqueueResult, error) {
	result, err := tv.EnqueueRefresh(ctx, p.queue)
	if err != nil {
		p.logger.Warn("enqueue tv refresh", slog.Any("error", err))
	}
	return result, err
}
