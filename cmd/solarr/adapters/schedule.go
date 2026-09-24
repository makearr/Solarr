package adapters

import (
	"context"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// Schedule satisfies apiserver.Scheduler over the jobs task registry and settings.Cache.
type Schedule struct {
	Registry *jobs.TaskRegistry
	States   *jobs.ScheduleStates
	Runners  map[string]*jobs.TaskRunner
	Cache    *settings.Cache
	Clock    clock.Clock
}

func (a Schedule) Tasks() []jobs.Task { return a.Registry.All() }

func (a Schedule) State(ctx context.Context, key string) (jobs.ScheduleRun, bool, error) {
	return a.States.Get(ctx, key)
}

func (a Schedule) EffectiveCadences(key, defaultCadence string) []string {
	return a.Cache.Snapshot().ScheduleCadences(key, defaultCadence)
}

func (a Schedule) RunAtStartup(key string, fallback bool) bool {
	return a.Cache.Snapshot().ScheduleRunAtStartup(key, fallback)
}

func (a Schedule) Enabled(key string) bool {
	return a.Cache.Snapshot().ScheduleEnabled(key)
}

func (a Schedule) SetCadences(ctx context.Context, key string, cadences []string) error {
	if _, found := a.Registry.Lookup(key); !found {
		return jobs.ErrUnknownTask
	}
	return a.Cache.SetScheduleCadences(ctx, a.Clock, key, cadences)
}

func (a Schedule) SetRunAtStartup(ctx context.Context, key string, run bool) error {
	if _, found := a.Registry.Lookup(key); !found {
		return jobs.ErrUnknownTask
	}
	return a.Cache.SetScheduleRunAtStartup(ctx, a.Clock, key, run)
}

func (a Schedule) SetEnabled(ctx context.Context, key string, enabled bool) error {
	if _, found := a.Registry.Lookup(key); !found {
		return jobs.ErrUnknownTask
	}
	return a.Cache.SetScheduleEnabled(ctx, a.Clock, key, enabled)
}

func (a Schedule) RunNow(ctx context.Context, key string, full bool) error {
	runner, found := a.Runners[key]
	if !found {
		return jobs.ErrUnknownTask
	}
	if full {
		return runner.RunNowFull(ctx)
	}
	return runner.RunNow(ctx)
}
