package app

import (
	"context"
	"log/slog"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// Default cadences for every registered task (ADR-59), gentler since 2026-09-24 and offset so
// no two land on the same minute; a stored settings.db override replaces them.
const (
	scanScheduleCron               = "0 */6 * * *"   // every 6 hours
	enrichmentScheduleCron         = "20 * * * *"    // every hour
	subscriptionExpandScheduleCron = "10 */12 * * *" // every 12 hours
	artworkScheduleCron            = "30 */6 * * *"  // every 6 hours
	tagWritebackScheduleCron       = "45 * * * *"    // every hour, paced to 2000 files a run
	discographyScheduleCron        = "0 4 * * 1"     // Mondays at 04:00; the provider caches, so only new artists cost requests
	statisticsScheduleCron         = "15 6 * * *"    // daily, after the counters; the statistics page reads what it wrote
	categoryCountsScheduleCron     = "30 5 * * *"    // daily, after database maintenance; only corrects drift
	freshCountsScheduleCron        = "40 * * * *"    // every hour
	probeScheduleCron              = "0 2 * * *"     // daily at 02:00
	capacityScheduleCron           = "50 */6 * * *"  // every 6 hours
	cacheMaintenanceScheduleCron   = "0 3 * * *"     // daily at 03:00
	downloadWatchScheduleCron      = "*/5 * * * *"   // every 5 minutes
	downloadPollScheduleCron       = "* * * * *"     // every minute, as Lidarr checks its clients
	databaseMaintenanceCron        = "0 4 * * 0"     // Sundays at 04:00, clear of the other nightly tasks
	backupScheduleCron             = "0 1 * * 0"     // Sundays at 01:00 (weekly, asked 2026-09-25)
	backupCleanupScheduleCron      = "30 1 * * 0"    // Sundays at 01:30, after the backup
	recycleBinCleanupScheduleCron  = "30 2 * * *"    // daily at 02:30
	refreshArtistsScheduleCron     = "0 5 * * 0"     // Sundays at 05:00, after database maintenance
	planScheduleCron               = "*/5 * * * *"   // every 5 minutes; reads the database only
	cutoffScheduleCron             = "40 2 * * *"    // daily at 02:40, after the probe measured the night's files
	rssScheduleCron                = "*/15 * * * *"  // every 15 minutes, as Lidarr's RSS Sync
	huntScheduleCron               = "*/15 * * * *"  // every 15 minutes, within the hourly budget
	tvRefreshScheduleCron          = "25 * * * *"    // every hour, 40 series a run: a running series is due twice a day
)

// buildTaskRegistry declares every periodic action this process has (ADR-59), in the order a
// startup run enqueues them.
func (p *process) buildTaskRegistry() *jobs.TaskRegistry {
	registry := jobs.NewTaskRegistry()
	registry.MustRegister(jobs.Task{
		Key: "scan", Name: "Library scan", DefaultCadence: scanScheduleCron,
		Tier: string(settings.TierNormal), Category: jobs.CategoryLibrary, Startup: jobs.StartupOn,
		Enqueue: p.enqueueLibraryScans, RunFull: p.enqueueFullLibraryScans,
	})
	registry.MustRegister(jobs.Task{
		Key: "enrichment", Name: "Music enrichment", DefaultCadence: enrichmentScheduleCron,
		EnqueueManual: p.enqueueManualEnrichment,
		Tier:          string(settings.TierNormal), Category: jobs.CategoryMetadata, Startup: jobs.StartupOn,
		Enqueue: p.enqueueEnrichment,
	})
	registry.MustRegister(jobs.Task{
		Key: "refresh_artists", Name: "Refresh artists", DefaultCadence: refreshArtistsScheduleCron,
		Tier: string(settings.TierNormal), Category: jobs.CategoryMetadata, Enqueue: p.enqueueRefreshArtists,
	})
	registry.MustRegister(jobs.Task{
		Key: "refresh_series", Name: "Refresh series", DefaultCadence: tvRefreshScheduleCron,
		Tier: string(settings.TierAdvanced), Category: jobs.CategoryMetadata, Enqueue: p.enqueueTVRefresh,
	})
	registry.MustRegister(jobs.Task{
		Key: "subscription_expand", Name: "Subscription expand", DefaultCadence: subscriptionExpandScheduleCron,
		Tier: string(settings.TierAdvanced), Category: jobs.CategoryMetadata, Startup: jobs.StartupOn,
		Enqueue: p.enqueueSubscriptionExpand,
	})
	registry.MustRegister(jobs.Task{
		Key: "artwork", Name: "Cover art sweep", DefaultCadence: artworkScheduleCron,
		Tier: string(settings.TierAdvanced), Category: jobs.CategoryMetadata, Startup: jobs.StartupOn,
		Enqueue: p.enqueueArtwork,
	})
	registry.MustRegister(jobs.Task{
		Key: "tag_writeback", Name: "Write tags into files", DefaultCadence: tagWritebackScheduleCron,
		Tier: string(settings.TierAdvanced), Category: jobs.CategoryLibrary, Startup: jobs.StartupOn,
		Enqueue: p.enqueueTagWriteback,
	})
	registry.MustRegister(jobs.Task{
		Key: "category_counts", Name: "Category counters", DefaultCadence: categoryCountsScheduleCron,
		Tier: string(settings.TierAdvanced), Category: jobs.CategoryStatistics, Startup: jobs.StartupOn,
		Enqueue: p.enqueueCategoryCounts,
	})
	registry.MustRegister(jobs.Task{
		Key: "discography_coverage", Name: "Compare discographies", DefaultCadence: discographyScheduleCron,
		Tier: string(settings.TierAdvanced), Category: jobs.CategoryMetadata, Startup: jobs.StartupOn,
		Enqueue: p.enqueueDiscographyCoverage,
	})
	registry.MustRegister(jobs.Task{
		Key: "statistics", Name: "Statistics", DefaultCadence: statisticsScheduleCron,
		Tier: string(settings.TierAdvanced), Category: jobs.CategoryStatistics, Startup: jobs.StartupOn,
		Enqueue: p.enqueueStatistics,
	})
	registry.MustRegister(jobs.Task{
		Key: "fresh_counts", Name: "Fresh works counter", DefaultCadence: freshCountsScheduleCron,
		Tier: string(settings.TierAdvanced), Category: jobs.CategoryStatistics, Startup: jobs.StartupOn,
		Enqueue: p.enqueueFreshCounts,
	})
	registry.MustRegister(jobs.Task{
		Key: "probe", Name: "Attribute probe", DefaultCadence: probeScheduleCron,
		Tier: string(settings.TierNormal), Category: jobs.CategoryLibrary, Startup: jobs.StartupOn,
		Enqueue: p.enqueueProbe,
	})
	registry.MustRegister(jobs.Task{
		Key: "capacity", Name: "Root folder capacity", DefaultCadence: capacityScheduleCron,
		Tier: string(settings.TierAdvanced), Category: jobs.CategoryStatistics, Startup: jobs.StartupOn,
		Enqueue: p.enqueueCapacity,
	})
	registry.MustRegister(jobs.Task{
		Key: "cache_maintenance", Name: "Cache maintenance", DefaultCadence: cacheMaintenanceScheduleCron,
		Tier: string(settings.TierAdvanced), Category: jobs.CategoryMaintenance, Startup: jobs.StartupOn,
		Enqueue: p.enqueueCacheMaintenance,
	})
	registry.MustRegister(jobs.Task{
		Key: "download_watch", Name: "Download folder watch", DefaultCadence: downloadWatchScheduleCron,
		Tier: string(settings.TierNormal), Category: jobs.CategoryDownloads, Startup: jobs.StartupOff,
		Enqueue: p.enqueueDownloadWatch,
	})
	registry.MustRegister(jobs.Task{
		Key: "download_poll", Name: "Check downloads", DefaultCadence: downloadPollScheduleCron,
		Tier: string(settings.TierAdvanced), Category: jobs.CategoryDownloads, Startup: jobs.StartupOn,
		Enqueue: p.enqueuePoll,
	})
	registry.MustRegister(jobs.Task{
		Key: "plan", Name: "Plan wanted albums", DefaultCadence: planScheduleCron,
		Tier: string(settings.TierAdvanced), Category: jobs.CategoryDownloads, Startup: jobs.StartupOn,
		Enqueue: p.enqueuePlan,
	})
	registry.MustRegister(jobs.Task{
		Key: "cutoff", Name: "Check quality cutoff", DefaultCadence: cutoffScheduleCron,
		Tier: string(settings.TierAdvanced), Category: jobs.CategoryLibrary, Startup: jobs.StartupOn,
		Enqueue: p.enqueueCutoff,
	})
	registry.MustRegister(jobs.Task{
		Key: "rss", Name: "RSS sync", DefaultCadence: rssScheduleCron,
		Tier: string(settings.TierNormal), Category: jobs.CategoryDownloads, Startup: jobs.StartupOff,
		Enqueue: p.enqueueRSS,
	})
	registry.MustRegister(jobs.Task{
		Key: "hunt", Name: "Search for missing albums", DefaultCadence: huntScheduleCron,
		Tier: string(settings.TierNormal), Category: jobs.CategoryDownloads, Startup: jobs.StartupOff,
		Enqueue: p.enqueueHunt,
	})
	registry.MustRegister(jobs.Task{
		Key: "database_maintenance", Name: "Database maintenance", DefaultCadence: databaseMaintenanceCron,
		Tier: string(settings.TierNormal), Category: jobs.CategoryMaintenance, Enqueue: p.enqueueDatabaseMaintenance,
	})
	registry.MustRegister(jobs.Task{
		Key: "backup", Name: "Backup", DefaultCadence: backupScheduleCron,
		Tier: string(settings.TierNormal), Category: jobs.CategoryMaintenance, Enqueue: p.enqueueBackup,
	})
	registry.MustRegister(jobs.Task{
		Key: "recycle_bin_cleanup", Name: "Clean recycling bin", DefaultCadence: recycleBinCleanupScheduleCron,
		Tier: string(settings.TierNormal), Category: jobs.CategoryMaintenance, Enqueue: p.enqueueRecycleBinCleanup,
	})
	registry.MustRegister(jobs.Task{
		Key: "backup_cleanup", Name: "Clean old backups", DefaultCadence: backupCleanupScheduleCron,
		Tier: string(settings.TierNormal), Category: jobs.CategoryMaintenance, Enqueue: p.enqueueBackupCleanup,
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

	for _, task := range p.taskRegistry.InRegistrationOrder() {
		runner := jobs.NewTaskRunner(task, source, p.scheduleStates, wall, p.logger)
		p.taskRunners[task.Key] = runner
		runner.Startup(ctx)
		go runner.Run(ctx)
	}
	p.logger.Info("schedulers started", slog.Int("tasks", len(p.taskRunners)))
}
