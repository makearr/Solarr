package app

import (
	"context"
	"log/slog"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/service/follow"
)

// followService is following for every media type: the API's follow, unfollow and preview, and the sweep.
func (p *process) followService() follow.Service {
	return follow.Service{
		Subscriptions: library.NewSubscriptions(p.library), Exclusions: library.NewExclusions(p.library), Media: p.media,
		Libraries: adapters.Categories{Cache: p.cache}, Queue: p.queue, Clock: clock.NewSystem(), Logger: p.logger,
	}
}

// registerFollowJob wires the sweep that adds the new works of every follow.
func (p *process) registerFollowJob(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(follow.Definition); err != nil {
		return err
	}
	executor.Handle(follow.JobType, p.followService())
	return nil
}

// enqueueFollowSweep submits one sweep; registered as a task, so it also runs on its own schedule.
func (p *process) enqueueFollowSweep(ctx context.Context) (jobs.EnqueueResult, error) {
	result, err := follow.Enqueue(ctx, p.queue, 0, false)
	if err != nil {
		p.logger.Warn("enqueue follow sweep", slog.Any("error", err))
	}
	return result, err
}
