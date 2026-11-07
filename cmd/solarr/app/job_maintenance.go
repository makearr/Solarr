package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/statistics"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// Database maintenance, backups and their cleanup, all scheduler tasks since 2026-09-24.
const (
	databaseMaintenanceJobType = jobs.Type("database.maintenance")
	backupJobType              = jobs.Type("database.backup")
	backupCleanupJobType       = jobs.Type("database.backup_cleanup")
	recycleBinCleanupJobType   = jobs.Type("library.recycle_bin_cleanup")
	decisionRetentionJobType   = jobs.Type("library.decision_retention")
	jobRetentionJobType        = jobs.Type("jobs.retention")
)

// decisionRetentionPages bounds one run, so a first pass over an old backlog is spread over several nights.
const decisionRetentionPages = 250

// finishedJobsKept is how long a finished job stays listed; without a limit jobs.db only grows, by thousands a day.
const finishedJobsKept = 14 * 24 * time.Hour

// downloadSpeedKept is how long a download's speed row stays; the page shows the last 30 days.
const downloadSpeedKept = 90 * 24 * time.Hour

// jobRetentionBatch and jobRetentionPages bound one run's transactions and its length (Invariant 2).
const (
	jobRetentionBatch = 200
	jobRetentionPages = 500
)

var maintenanceDefinitions = []jobs.Definition{
	{Type: databaseMaintenanceJobType, Name: "Maintaining the databases", Pool: jobs.PoolMaintenance, Mutating: false, MaxAttempts: 1},
	{Type: backupJobType, Name: "Backing up", Pool: jobs.PoolMaintenance, Mutating: false, MaxAttempts: 3},
	{Type: backupCleanupJobType, Pool: jobs.PoolHousekeeping, Mutating: true, MaxAttempts: 3},
	{Type: recycleBinCleanupJobType, Pool: jobs.PoolHousekeeping, Mutating: true, MaxAttempts: 3},
	{Type: decisionRetentionJobType, Name: "Cleaning old search results", Pool: jobs.PoolHousekeeping, Mutating: true, MaxAttempts: 3},
	{Type: jobRetentionJobType, Name: "Cleaning old jobs", Pool: jobs.PoolHousekeeping, Mutating: true, MaxAttempts: 3},
}

// decisionRetentionJob ages search decisions past retention.decisions_full into the daily rollup and deletes the
// expired releases nothing refers to any more (section 5.7); without it both tables only grow.
type decisionRetentionJob struct {
	candidates statistics.Candidates
	speeds     statistics.DownloadSpeeds
	keepFull   time.Duration
	clock      clock.Clock
	logger     *slog.Logger
}

func (j decisionRetentionJob) Run(ctx context.Context, _ jobs.Job) error {
	now := j.clock.Now()
	retired, err := j.candidates.Retire(ctx, now, now.Add(-j.keepFull), decisionRetentionPages)
	if err != nil {
		return err
	}
	if _, err := j.speeds.Retire(ctx, now.Add(-downloadSpeedKept), decisionRetentionPages); err != nil {
		return err
	}
	if retired.Decisions > 0 || retired.Candidates > 0 {
		j.logger.Info("old search results cleaned", slog.Int64("decisions", retired.Decisions), slog.Int64("releases", retired.Candidates))
	}
	return nil
}

// jobRetentionJob deletes jobs finished more than finishedJobsKept ago, a page at a time.
type jobRetentionJob struct {
	queue  *jobs.Queue
	clock  clock.Clock
	logger *slog.Logger
}

func (j jobRetentionJob) Run(ctx context.Context, _ jobs.Job) error {
	before := j.clock.Now().Add(-finishedJobsKept)
	var total int64
	for range jobRetentionPages {
		deleted, err := j.queue.PruneFinished(ctx, before, jobRetentionBatch)
		if err != nil {
			return err
		}
		total += deleted
		if deleted < jobRetentionBatch {
			break
		}
	}
	if total > 0 {
		j.logger.Info("old jobs cleaned", slog.Int64("jobs", total))
	}
	return nil
}

// recycleBinCleanupJob purges recycle bin entries past the retention their library gave them
// (section 10.8; Sonarr's recycling bin cleanup).
type recycleBinCleanupJob struct {
	recycleBin library.RecycleBin
	cache      *settings.Cache
	logger     *slog.Logger
}

func (j recycleBinCleanupJob) Run(ctx context.Context, _ jobs.Job) error {
	snapshot := j.cache.Snapshot()
	limits := snapshot.RecycleBinLimits()
	var rootFolderIDs []int64
	for _, root := range snapshot.RootFolders() {
		rootFolderIDs = append(rootFolderIDs, root.ID)
	}
	capacity := func(rootFolderID int64) (int64, bool) {
		root, found := snapshot.RootFolder(rootFolderID)
		if !found || root.TotalBytes == nil {
			return 0, false
		}
		return *root.TotalBytes, true
	}
	free := func(rootFolderID int64) (int64, int64, bool) {
		root, found := snapshot.RootFolder(rootFolderID)
		if !found || root.TotalBytes == nil || root.FreeBytes == nil {
			return 0, 0, false
		}
		return *root.FreeBytes, *root.TotalBytes, true
	}
	result, err := j.recycleBin.Sweep(ctx, library.RecycleBinConfig{
		MaxBytes: limits.MaxBytes, MaxPercentOfRoot: float64(limits.MaxPercentOfRoot),
		MinFreeBytes: limits.MinFreeBytes, MinFreePercent: float64(limits.MinFreePercent), Free: free,
	}, capacity, rootFolderIDs)
	if err != nil {
		return err
	}
	for _, failure := range result.Errors {
		j.logger.Warn("recycling bin: could not purge an entry", slog.Any("error", failure))
	}
	if result.Purged > 0 {
		j.logger.Info("recycling bin cleaned", slog.Int("purged", result.Purged), slog.Int64("bytes_freed", result.BytesFreed))
	}
	return nil
}

// databaseMaintenanceJob runs Store.Maintain on every database in turn, so only one is paused at
// a time. A failure on one is reported and the others still run.
type databaseMaintenanceJob struct {
	stores []*store.Store
	logger *slog.Logger
}

func (j databaseMaintenanceJob) Run(ctx context.Context, _ jobs.Job) error {
	var failures []error
	for _, target := range j.stores {
		report, err := target.Maintain(ctx)
		if err != nil {
			failures = append(failures, err)
			j.logger.Error("database maintenance failed", slog.String("database", target.Name()), slog.Any("error", err))
			continue
		}
		j.logger.Info("database maintained", slog.String("database", report.Database),
			slog.Int64("bytes_before", report.SizeBefore), slog.Int64("bytes_after", report.SizeAfter),
			slog.Duration("duration", report.Duration), slog.Bool("checkpoint_blocked", report.Checkpoint.Blocked))
	}
	return errors.Join(failures...)
}

// backupJob takes one verified backup bundle of every database and config.yml.
type backupJob struct {
	backup bundleBackup
	logger *slog.Logger
}

func (j backupJob) Run(ctx context.Context, _ jobs.Job) error {
	path, err := j.backup.Take(ctx)
	if err != nil {
		return err
	}
	j.logger.Info("scheduled backup", slog.String("path", path))
	return nil
}

// backupCleanupJob applies the configured retention to the backup bundles and each database's older single copies.
type backupCleanupJob struct {
	fs        fsx.FS
	stores    []*store.Store
	directory string
	cache     *settings.Cache
	clock     clock.Clock
	logger    *slog.Logger
}

func (j backupCleanupJob) Run(_ context.Context, _ jobs.Job) error {
	retention := j.cache.Snapshot().BackupRetention()
	var bundles []string
	var err error
	if retention.Mode == settings.BackupKeepDays {
		bundles, err = store.PruneBundles(j.fs, j.directory, 0, j.clock.Now().Add(-time.Duration(retention.KeepDays)*24*time.Hour))
	} else {
		bundles, err = store.PruneBundles(j.fs, j.directory, int(retention.KeepCount), time.Time{})
	}
	if err != nil {
		return fmt.Errorf("clean backup bundles: %w", err)
	}
	if len(bundles) > 0 {
		j.logger.Info("old backups removed", slog.Int("count", len(bundles)))
	}
	for _, target := range j.stores {
		var removed []string
		var err error
		if retention.Mode == settings.BackupKeepDays {
			cutoff := j.clock.Now().Add(-time.Duration(retention.KeepDays) * 24 * time.Hour)
			removed, err = target.PruneBackupsOlderThan(j.directory, cutoff)
		} else {
			removed, err = target.PruneBackups(j.directory, int(retention.KeepCount))
		}
		if err != nil {
			return fmt.Errorf("clean backups of %s: %w", target.Name(), err)
		}
		if len(removed) > 0 {
			j.logger.Info("old backups removed", slog.String("database", target.Name()), slog.Int("count", len(removed)))
		}
	}
	return nil
}

// registerMaintenanceJobs wires the maintenance jobs into the registry and the executor.
func (p *process) registerMaintenanceJobs(registry *jobs.Registry, executor *jobs.Executor) error {
	for _, definition := range maintenanceDefinitions {
		if err := registry.Register(definition); err != nil {
			return err
		}
	}
	// settings.db backs itself up on every change and prunes by keep_settings, so it is left out.
	backedUp := []*store.Store{p.library, p.jobs, p.statistics}
	executor.Handle(databaseMaintenanceJobType, databaseMaintenanceJob{
		stores: []*store.Store{p.settings, p.jobs, p.library, p.statistics, p.metadata}, logger: p.logger,
	})
	executor.Handle(recycleBinCleanupJobType, recycleBinCleanupJob{
		recycleBin: library.NewRecycleBin(p.library, p.fsys, clock.NewSystem()), cache: p.cache, logger: p.logger,
	})
	executor.Handle(decisionRetentionJobType, decisionRetentionJob{
		candidates: statistics.NewCandidates(p.statistics), speeds: statistics.NewDownloadSpeeds(p.statistics), keepFull: p.config.Retention.DecisionsFull.AsDuration(),
		clock: clock.NewSystem(), logger: p.logger,
	})
	executor.Handle(jobRetentionJobType, jobRetentionJob{queue: p.queue, clock: clock.NewSystem(), logger: p.logger})
	executor.Handle(backupJobType, backupJob{
		backup: bundleBackup{
			FS: p.fsys, Directory: p.config.Paths.BackupDir, ConfigPath: p.configPath, Clock: clock.NewSystem(),
			Stores: []*store.Store{p.settings, p.jobs, p.library, p.statistics},
		},
		logger: p.logger,
	})
	executor.Handle(backupCleanupJobType, backupCleanupJob{
		fs: p.fsys, stores: backedUp, directory: p.config.Paths.BackupDir, cache: p.cache, clock: clock.NewSystem(), logger: p.logger,
	})
	return nil
}

func (p *process) enqueueMaintenanceJob(ctx context.Context, jobType jobs.Type) (jobs.EnqueueResult, error) {
	result, err := p.queue.Enqueue(ctx, jobs.Request{
		Type: jobType, ActionKey: string(jobType), Priority: jobs.PriorityLow, Payload: []byte("{}"),
	})
	if err != nil {
		p.logger.Warn("enqueue maintenance", slog.String("type", string(jobType)), slog.Any("error", err))
	}
	return result, err
}

func (p *process) enqueueDatabaseMaintenance(ctx context.Context) (jobs.EnqueueResult, error) {
	return p.enqueueMaintenanceJob(ctx, databaseMaintenanceJobType)
}

func (p *process) enqueueBackup(ctx context.Context) (jobs.EnqueueResult, error) {
	return p.enqueueMaintenanceJob(ctx, backupJobType)
}

func (p *process) enqueueRecycleBinCleanup(ctx context.Context) (jobs.EnqueueResult, error) {
	return p.enqueueMaintenanceJob(ctx, recycleBinCleanupJobType)
}

func (p *process) enqueueDecisionRetention(ctx context.Context) (jobs.EnqueueResult, error) {
	return p.enqueueMaintenanceJob(ctx, decisionRetentionJobType)
}

func (p *process) enqueueJobRetention(ctx context.Context) (jobs.EnqueueResult, error) {
	return p.enqueueMaintenanceJob(ctx, jobRetentionJobType)
}

func (p *process) enqueueBackupCleanup(ctx context.Context) (jobs.EnqueueResult, error) {
	return p.enqueueMaintenanceJob(ctx, backupCleanupJobType)
}
