package main

import (
	"context"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/jobs"
	"github.com/makearr/MediaNexus/internal/settings"
)

// scheduleAdapter satisfies apiserver.Scheduler over internal/jobs.
// TaskRegistry/ScheduleStates/TaskRunner and internal/settings.Cache, the
// same interfaces-in-apiserver/adapters-in-cmd/medianexus shape every other
// library-backed endpoint already uses -- apiserver never imports
// cmd/medianexus.
type scheduleAdapter struct {
	registry *jobs.TaskRegistry
	states   *jobs.ScheduleStates
	runners  map[string]*jobs.TaskRunner
	cache    *settings.Cache
	clock    clock.Clock
}

func (a scheduleAdapter) Tasks() []jobs.Task { return a.registry.All() }

func (a scheduleAdapter) State(ctx context.Context, key string) (jobs.ScheduleRun, bool, error) {
	return a.states.Get(ctx, key)
}

func (a scheduleAdapter) EffectiveCadence(key, defaultCadence string) string {
	return a.cache.Snapshot().ScheduleCadence(key, defaultCadence)
}

func (a scheduleAdapter) Enabled(key string) bool {
	return a.cache.Snapshot().ScheduleEnabled(key)
}

func (a scheduleAdapter) SetCadence(ctx context.Context, key, cadence string) error {
	if _, found := a.registry.Lookup(key); !found {
		return jobs.ErrUnknownTask
	}
	return a.cache.SetScheduleCadence(ctx, a.clock, key, cadence)
}

func (a scheduleAdapter) SetEnabled(ctx context.Context, key string, enabled bool) error {
	if _, found := a.registry.Lookup(key); !found {
		return jobs.ErrUnknownTask
	}
	return a.cache.SetScheduleEnabled(ctx, a.clock, key, enabled)
}

func (a scheduleAdapter) RunNow(ctx context.Context, key string) error {
	runner, found := a.runners[key]
	if !found {
		return jobs.ErrUnknownTask
	}
	return runner.RunNow(ctx)
}
