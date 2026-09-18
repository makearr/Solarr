package app

import (
	"context"
	"log/slog"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
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
	freshCountsScheduleCron        = "*/15 * * * *" // every 15 minutes
	probeScheduleCron              = "0 * * * *"    // every hour
	capacityScheduleCron           = "0 */6 * * *"  // every 6 hours
	cacheMaintenanceScheduleCron   = "0 3 * * *"    // once a day, at 03:00
)

// buildTaskRegistry declares every periodic action this process has (ADR-59).
// Every enqueueX function used to be "enqueued once at startup, waits for a
// restart" (section 21.14's own words for the gap this closes) -- each is
// now one Task, registered once, that also fires on its own schedule.
func (p *process) buildTaskRegistry() *jobs.TaskRegistry {
	registry := jobs.NewTaskRegistry()
	registry.MustRegister(jobs.Task{
		Key: "scan", Name: "Library scan", DefaultCadence: scanScheduleCron,
		Tier: string(settings.TierNormal), Enqueue: p.enqueueLibraryScans,
		// Full rescan (private/questions.md): manual only, deliberately scan's
		// alone -- see Task.RunFull's own doc comment for why this stays
		// narrow rather than a generic options payload every task would grow
		// a case for.
		RunFull: p.enqueueFullLibraryScans,
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
		Key: "fresh_counts", Name: "Fresh works counter", DefaultCadence: freshCountsScheduleCron,
		Tier: string(settings.TierAdvanced), Enqueue: p.enqueueFreshCounts,
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
	source := adapters.ScheduleSource{Cache: p.cache}

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
