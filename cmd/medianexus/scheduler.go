package main

import (
	"context"
	"log/slog"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/jobs"
	"github.com/makearr/MediaNexus/internal/settings"
)

// Default cadences for every registered task (ADR-59), the starting point a
// stored settings.db override replaces. Named here rather than buried in
// buildTaskRegistry, so a reader can see every task's own default in one
// place.
const (
	scanScheduleCron               = "*/30 * * * *" // every 30 minutes
	enrichmentScheduleCron         = "*/15 * * * *" // every 15 minutes
	subscriptionExpandScheduleCron = "0 */6 * * *"  // every 6 hours
	artworkScheduleCron            = "*/30 * * * *" // every 30 minutes
	categoryCountsScheduleCron     = "*/15 * * * *" // every 15 minutes
	probeScheduleCron              = "0 * * * *"    // every hour
	capacityScheduleCron           = "0 */6 * * *"  // every 6 hours
	cacheMaintenanceScheduleCron   = "0 3 * * *"    // once a day, at 03:00
)

// cacheScheduleSource adapts settings.Cache to jobs.ScheduleSource -- the
// snapshot is re-read on every call, so a cadence or enabled-flag change
// takes effect on the task's own next tick, no restart needed.
type cacheScheduleSource struct{ cache *settings.Cache }

func (c cacheScheduleSource) Cadence(taskKey, defaultCadence string) string {
	return c.cache.Snapshot().ScheduleCadence(taskKey, defaultCadence)
}

func (c cacheScheduleSource) Enabled(taskKey string) bool {
	return c.cache.Snapshot().ScheduleEnabled(taskKey)
}

// buildTaskRegistry declares every periodic action this process has (ADR-59).
// Every enqueueX function used to be "enqueued once at startup, waits for a
// restart" (section 21.14's own words for the gap this closes) -- each is
// now one Task, registered once, that also fires on its own schedule.
func (p *process) buildTaskRegistry() *jobs.TaskRegistry {
	registry := jobs.NewTaskRegistry()
	registry.MustRegister(jobs.Task{
		Key: "scan", Name: "Library scan", DefaultCadence: scanScheduleCron,
		Tier: string(settings.TierNormal), Enqueue: p.enqueueLibraryScans,
	})
	registry.MustRegister(jobs.Task{
		Key: "enrichment", Name: "Music enrichment", DefaultCadence: enrichmentScheduleCron,
		Tier: string(settings.TierNormal), Enqueue: p.enqueueEnrichment,
	})
	registry.MustRegister(jobs.Task{
		Key: "subscription_expand", Name: "Subscription expand", DefaultCadence: subscriptionExpandScheduleCron,
		Tier: string(settings.TierAdvanced), Enqueue: p.enqueueSubscriptionExpand,
	})
	registry.MustRegister(jobs.Task{
		Key: "artwork", Name: "Cover art sweep", DefaultCadence: artworkScheduleCron,
		Tier: string(settings.TierAdvanced), Enqueue: p.enqueueArtwork,
	})
	registry.MustRegister(jobs.Task{
		Key: "category_counts", Name: "Category counters", DefaultCadence: categoryCountsScheduleCron,
		Tier: string(settings.TierAdvanced), Enqueue: p.enqueueCategoryCounts,
	})
	registry.MustRegister(jobs.Task{
		Key: "probe", Name: "Attribute probe", DefaultCadence: probeScheduleCron,
		Tier: string(settings.TierAdvanced), Enqueue: p.enqueueProbe,
	})
	registry.MustRegister(jobs.Task{
		Key: "capacity", Name: "Root folder capacity", DefaultCadence: capacityScheduleCron,
		Tier: string(settings.TierAdvanced), Enqueue: p.enqueueCapacity,
	})
	registry.MustRegister(jobs.Task{
		Key: "cache_maintenance", Name: "Cache maintenance", DefaultCadence: cacheMaintenanceScheduleCron,
		Tier: string(settings.TierAdvanced), Enqueue: p.enqueueCacheMaintenance,
	})
	return registry
}

// startSchedulers builds one TaskRunner per registered task and drives each
// on its own goroutine, the same per-task isolation the old flat Scheduler
// loop already had: one misbehaving task (an invalid override, refused at
// TaskRunner.resolveSchedule's own fallback) never affects another.
func (p *process) startSchedulers(ctx context.Context) {
	wall := clock.NewSystem()
	source := cacheScheduleSource{cache: p.cache}

	p.taskRegistry = p.buildTaskRegistry()
	p.scheduleStates = jobs.NewScheduleStates(p.jobs)
	p.taskRunners = make(map[string]*jobs.TaskRunner)

	for _, task := range p.taskRegistry.All() {
		runner := jobs.NewTaskRunner(task, source, p.scheduleStates, wall, p.logger)
		p.taskRunners[task.Key] = runner
		go runner.Run(ctx)
	}
	p.logger.Info("schedulers started", slog.Int("tasks", len(p.taskRunners)))
}
