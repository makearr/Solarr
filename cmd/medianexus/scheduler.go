package main

import (
	"context"
	"log/slog"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/jobs"
)

// Default cron expressions for every job that used to be "enqueued once at
// startup, waits for a restart" (section 21.14's own words for the gap this
// closes). Not yet configurable -- a fixed, reasonable default for each,
// named here rather than buried in startSchedulers, so a future settings
// registry (todo 4.14) has one obvious place to read a starting point from.
const (
	scanScheduleCron               = "*/30 * * * *" // every 30 minutes
	enrichmentScheduleCron         = "*/15 * * * *" // every 15 minutes
	subscriptionExpandScheduleCron = "0 */6 * * *"  // every 6 hours
	artworkScheduleCron            = "*/30 * * * *" // every 30 minutes
	categoryCountsScheduleCron     = "*/15 * * * *" // every 15 minutes
	probeScheduleCron              = "0 * * * *"    // every hour
	capacityScheduleCron           = "0 */6 * * *"  // every 6 hours
)

// startSchedulers periodically re-runs every enqueue function startServer
// already calls once at startup, so a placeholder edition, a newly-dropped
// file or a subscription's new release does not wait for a restart to be
// picked up. Each is its own goroutine, the same shape store.BackupSchedule
// already uses per store -- one misbehaving schedule (an invalid expression,
// which NewScheduler already refused at construction) never affects another.
func (p *process) startSchedulers(ctx context.Context) {
	wall := clock.NewSystem()
	schedules := []struct {
		name string
		cron string
		fire func(context.Context)
	}{
		{"scan", scanScheduleCron, p.enqueueLibraryScans},
		{"enrichment", enrichmentScheduleCron, p.enqueueEnrichment},
		{"subscription_expand", subscriptionExpandScheduleCron, p.enqueueSubscriptionExpand},
		{"artwork", artworkScheduleCron, p.enqueueArtwork},
		{"category_counts", categoryCountsScheduleCron, p.enqueueCategoryCounts},
		{"probe", probeScheduleCron, p.enqueueProbe},
		{"capacity", capacityScheduleCron, p.enqueueCapacity},
	}

	for _, s := range schedules {
		schedule, err := jobs.NewScheduler(s.name, s.cron, wall, p.logger, s.fire)
		if err != nil {
			// Every cron expression above is a compile-time constant; this
			// can only fire if one of them is malformed, which is a bug in
			// this file, not a runtime condition -- logged loudly rather
			// than silently skipping the schedule.
			p.logger.Error("scheduler misconfigured, this schedule will never fire",
				slog.String("schedule", s.name), slog.Any("error", err))
			continue
		}
		go schedule.Run(ctx)
	}
}
