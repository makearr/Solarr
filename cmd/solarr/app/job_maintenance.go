package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// Database maintenance, backups and their cleanup, all scheduler tasks since 2026-09-24.
const (
	databaseMaintenanceJobType = jobs.Type("database.maintenance")
	backupJobType              = jobs.Type("database.backup")
	backupCleanupJobType       = jobs.Type("database.backup_cleanup")
)

var maintenanceDefinitions = []jobs.Definition{
	{Type: databaseMaintenanceJobType, Pool: jobs.PoolMaintenance, Mutating: false, MaxAttempts: 1},
	{Type: backupJobType, Pool: jobs.PoolMaintenance, Mutating: false, MaxAttempts: 3},
	{Type: backupCleanupJobType, Pool: jobs.PoolMaintenance, Mutating: true, MaxAttempts: 3},
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

// backupJob takes one rotation backup of each database it covers.
type backupJob struct {
	stores    []*store.Store
	directory string
	logger    *slog.Logger
}

func (j backupJob) Run(ctx context.Context, _ jobs.Job) error {
	for _, target := range j.stores {
		path, err := target.Backup(ctx, j.directory, store.BackupRotation)
		if err != nil {
			return fmt.Errorf("back up %s: %w", target.Name(), err)
		}
		j.logger.Info("scheduled backup", slog.String("database", target.Name()), slog.String("path", path))
	}
	return nil
}

// backupCleanupJob applies the configured retention to each database's rotation backups.
type backupCleanupJob struct {
	stores    []*store.Store
	directory string
	cache     *settings.Cache
	clock     clock.Clock
	logger    *slog.Logger
}

func (j backupCleanupJob) Run(_ context.Context, _ jobs.Job) error {
	retention := j.cache.Snapshot().BackupRetention()
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

// registerMaintenanceJobs wires the three into the registry and the executor.
func (p *process) registerMaintenanceJobs(registry *jobs.Registry, executor *jobs.Executor) error {
	for _, definition := range maintenanceDefinitions {
		if err := registry.Register(definition); err != nil {
			return err
		}
	}
	// settings.db backs itself up on every change and prunes by keep_settings, so it is left out.
	backedUp := []*store.Store{p.library, p.jobs}
	executor.Handle(databaseMaintenanceJobType, databaseMaintenanceJob{
		stores: []*store.Store{p.settings, p.jobs, p.library, p.metadata}, logger: p.logger,
	})
	executor.Handle(backupJobType, backupJob{stores: backedUp, directory: p.config.Paths.BackupDir, logger: p.logger})
	executor.Handle(backupCleanupJobType, backupCleanupJob{
		stores: backedUp, directory: p.config.Paths.BackupDir, cache: p.cache, clock: clock.NewSystem(), logger: p.logger,
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

func (p *process) enqueueBackupCleanup(ctx context.Context) (jobs.EnqueueResult, error) {
	return p.enqueueMaintenanceJob(ctx, backupCleanupJobType)
}
