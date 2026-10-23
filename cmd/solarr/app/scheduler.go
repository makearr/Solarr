package app

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// Default cadences for every registered task (ADR-59), gentler since 2026-09-24 and offset so
// no two land on the same minute; a stored settings.db override replaces them.
const (
	scanScheduleCron               = "0 */6 * * *"   // every 6 hours
	refreshWorksScheduleCron       = "20 * * * *"    // every hour; a running series is due twice a day, 40 a run
	subscriptionExpandScheduleCron = "10 */12 * * *" // every 12 hours
	picturesScheduleCron           = "10 * * * *"    // hourly; 1,500 series pictures a run until the library's are all kept
	tagWritebackScheduleCron       = "45 * * * *"    // every hour, paced to 2000 files a run
	discographyScheduleCron        = "0 4 * * 1"     // Mondays at 04:00; the provider caches, so only new artists cost requests
	statisticsScheduleCron         = "15 6 * * *"    // daily, after the counters; the statistics page reads what it wrote
	categoryCountsScheduleCron     = "30 5 * * *"    // daily, after database maintenance; only corrects drift
	freshCountsScheduleCron        = "40 * * * *"    // every hour
	comingOutScheduleCron          = "*/15 * * * *"  // every 15 minutes
	serverFilesScheduleCron        = "30 4 * * *"    // daily at 04:30
	probeScheduleCron              = "0 2 * * *"     // daily at 02:00
	capacityScheduleCron           = "50 */6 * * *"  // every 6 hours
	reachScheduleCron              = "*/5 * * * *"   // every 5 minutes; a stat and a listing per root
	cacheMaintenanceScheduleCron   = "0 3 * * *"     // daily at 03:00
	downloadWatchScheduleCron      = "*/5 * * * *"   // every 5 minutes
	downloadPollScheduleCron       = "* * * * *"     // every minute, as Lidarr checks its clients
	databaseMaintenanceCron        = "0 4 * * 0"     // Sundays at 04:00, clear of the other nightly tasks
	backupScheduleCron             = "0 1 * * 0"     // Sundays at 01:00 (weekly, asked 2026-09-25)
	backupCleanupScheduleCron      = "30 1 * * 0"    // Sundays at 01:30, after the backup
	recycleBinCleanupScheduleCron  = "30 2 * * *"    // daily at 02:30
	decisionRetentionScheduleCron  = "10 3 * * *"    // daily at 03:10
	refreshPeopleScheduleCron      = "0 5 * * 0"     // Sundays at 05:00, after database maintenance
	planScheduleCron               = "*/5 * * * *"   // every 5 minutes; reads the database only
	cutoffScheduleCron             = "40 2 * * *"    // daily at 02:40, after the probe measured the night's files
	rssScheduleCron                = "*/15 * * * *"  // every 15 minutes, as Lidarr's RSS Sync
	huntScheduleCron               = "*/15 * * * *"  // every 15 minutes, within the hourly budget
	crossoverPassScheduleCron      = "10 5 * * *"    // daily at 05:10, after the night's refreshes
	universePassScheduleCron       = "40 4 * * *"    // daily; one bulk read of Wikidata for the whole film library
	franchisePassScheduleCron      = "20 */4 * * *"  // every four hours; 60 series a run, Wikidata asks for polite use
	bridgeScheduleCron             = "50 3 * * *"    // daily at 03:50; 300 people a run, after the night's refreshes
	tvRetryScheduleCron            = "30 4 * * *"    // daily at 04:30: providers learn new series and fix old data overnight
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
		Key: "refresh_works", Name: "Refresh works", DefaultCadence: refreshWorksScheduleCron,
		Tier: string(settings.TierNormal), Category: jobs.CategoryMetadata, Startup: jobs.StartupOn,
		Enqueue: p.passEnqueue(media.PassWorks, false), EnqueueManual: p.passEnqueue(media.PassWorks, true),
	})
	registry.MustRegister(jobs.Task{
		Key: "refresh_people", Name: "Refresh people", DefaultCadence: refreshPeopleScheduleCron,
		Tier: string(settings.TierNormal), Category: jobs.CategoryMetadata,
		Enqueue: p.passEnqueue(media.PassPeople, false), EnqueueManual: p.passEnqueue(media.PassPeople, true),
	})
	registry.MustRegister(jobs.Task{
		Key: "pictures", Name: "Fetch pictures", DefaultCadence: picturesScheduleCron,
		Tier: string(settings.TierAdvanced), Category: jobs.CategoryMetadata, Startup: jobs.StartupOn,
		Enqueue: p.passEnqueue(media.PassPictures, false), EnqueueManual: p.passEnqueue(media.PassPictures, true),
	})
	registry.MustRegister(p.tvRetryTask())
	registry.MustRegister(jobs.Task{
		Key: "tv_crossovers", Name: "Find crossover episodes", DefaultCadence: crossoverPassScheduleCron,
		Tier: string(settings.TierAdvanced), Category: jobs.CategoryLibrary, Startup: jobs.StartupOn,
		Enqueue: p.enqueueCrossoverPass,
	})
	registry.MustRegister(jobs.Task{
		Key: "tv_franchises", Name: "Find the franchises series belong to", DefaultCadence: franchisePassScheduleCron,
		Tier: string(settings.TierAdvanced), Category: jobs.CategoryMetadata, Enqueue: p.enqueueFranchisePass,
	})
	registry.MustRegister(jobs.Task{
		Key: "movie_universes", Name: "Group films into franchises and universes", DefaultCadence: universePassScheduleCron,
		Tier: string(settings.TierAdvanced), Category: jobs.CategoryMetadata, Enqueue: p.enqueueUniversePass,
	})
	registry.MustRegister(jobs.Task{
		Key: "people_bridge", Name: "Match people across libraries", DefaultCadence: bridgeScheduleCron,
		Tier: string(settings.TierAdvanced), Category: jobs.CategoryMetadata, Enqueue: p.enqueueBridge,
	})
	registry.MustRegister(jobs.Task{
		Key: "subscription_expand", Name: "Add the works of followed people", DefaultCadence: subscriptionExpandScheduleCron,
		Tier: string(settings.TierAdvanced), Category: jobs.CategoryMetadata, Startup: jobs.StartupOn,
		Enqueue: p.enqueueFollowSweep,
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
		Key: "coming_out", Name: "Count what has come out", DefaultCadence: comingOutScheduleCron,
		Tier: string(settings.TierAdvanced), Category: jobs.CategoryLibrary, Startup: jobs.StartupOn,
		Enqueue: p.enqueueComingOut,
	})
	registry.MustRegister(jobs.Task{
		Key: "media_server_files", Name: "Write Jellyfin and Kodi files", DefaultCadence: serverFilesScheduleCron,
		Tier: string(settings.TierAdvanced), Category: jobs.CategoryLibrary, Startup: jobs.StartupOff,
		Enqueue: p.enqueueServerFiles,
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
		Key: "reach", Name: "Root folder reachability", DefaultCadence: reachScheduleCron,
		Tier: string(settings.TierAdvanced), Category: jobs.CategoryMaintenance, Startup: jobs.StartupOn,
		Enqueue: p.enqueueReach,
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
		Key: "plan", Name: "Plan what is wanted", DefaultCadence: planScheduleCron,
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
		Key: "hunt", Name: "Search for what is missing", DefaultCadence: huntScheduleCron,
		Tier: string(settings.TierNormal), Category: jobs.CategoryDownloads, Startup: jobs.StartupOff,
		Enqueue: p.enqueueHunt,
	})
	registry.MustRegister(jobs.Task{
		Key: "database_maintenance", Name: "Database maintenance", DefaultCadence: databaseMaintenanceCron,
		Tier: string(settings.TierNormal), Category: jobs.CategoryMaintenance, Enqueue: p.enqueueDatabaseMaintenance,
		StartupDelay: heavyStartupDelay,
	})
	registry.MustRegister(jobs.Task{
		Key: "backup", Name: "Backup", DefaultCadence: backupScheduleCron,
		Tier: string(settings.TierNormal), Category: jobs.CategoryMaintenance, Enqueue: p.enqueueBackup,
		StartupDelay: heavyStartupDelay,
	})
	registry.MustRegister(jobs.Task{
		Key: "recycle_bin_cleanup", Name: "Clean recycling bin", DefaultCadence: recycleBinCleanupScheduleCron,
		Tier: string(settings.TierNormal), Category: jobs.CategoryMaintenance, Enqueue: p.enqueueRecycleBinCleanup,
	})
	registry.MustRegister(jobs.Task{
		Key: "decision_retention", Name: "Clean old search results", DefaultCadence: decisionRetentionScheduleCron,
		Tier: string(settings.TierAdvanced), Category: jobs.CategoryMaintenance, Enqueue: p.enqueueDecisionRetention,
	})
	registry.MustRegister(jobs.Task{
		Key: "backup_cleanup", Name: "Clean old backups", DefaultCadence: backupCleanupScheduleCron,
		Tier: string(settings.TierNormal), Category: jobs.CategoryMaintenance, Enqueue: p.enqueueBackupCleanup,
	})
	return registry
}

// heavyStartupDelay lets the process settle before a startup run of database maintenance or a backup.
const heavyStartupDelay = 5 * time.Minute

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
		if task.StartupDelay > 0 {
			go runner.Startup(ctx)
		} else {
			runner.Startup(ctx)
		}
		go runner.Run(ctx)
	}
	p.logger.Info("schedulers started", slog.Int("tasks", len(p.taskRunners)))
}

// passEnqueue starts one metadata pass in every media type that has it, as one task: duplicate only when every type's
// pass was already on its way, every type's error joined.
func (p *process) passEnqueue(pass media.Pass, manual bool) func(context.Context) (jobs.EnqueueResult, error) {
	return func(ctx context.Context) (jobs.EnqueueResult, error) {
		var errs []error
		started, duplicates := 0, 0
		for _, descriptor := range p.media.Descriptors() {
			enriching, ok := media.Capability[media.Enriching](p.media, descriptor.Key)
			if !ok {
				continue
			}
			ran, duplicate, err := enriching.StartPass(ctx, pass, manual)
			if err != nil {
				p.logger.Warn("start metadata pass", slog.String("pass", string(pass)), slog.String("media_type", string(descriptor.Key)), slog.Any("error", err))
				errs = append(errs, err)
				continue
			}
			if ran {
				started++
			}
			if ran && duplicate {
				duplicates++
			}
		}
		return jobs.EnqueueResult{Duplicate: started > 0 && duplicates == started}, errors.Join(errs...)
	}
}
