package main

import (
	"context"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/store"
)

// startBackupSchedules wires ADR-15's routine rotation: library.db and jobs.db back
// up and prune on config.yml's backup.schedule cron expression, settings.db backs up
// on every write instead (settings_on_change -- it is tiny and precious, ADR-25).
// An empty schedule disables the cron half; SettingsOnChange disables the other.
// Both are best-effort background work: a missed backup is logged, never fatal, and
// store.Close already drains an in-flight one before the database closes.
func (p *process) startBackupSchedules(ctx context.Context) error {
	if p.config.Backup.Schedule != "" {
		for _, target := range []*store.Store{p.library, p.jobs} {
			schedule, err := store.NewBackupSchedule(
				target, p.config.Backup.Schedule, p.config.Paths.BackupDir,
				p.config.Backup.Keep, clock.NewSystem(), p.logger,
			)
			if err != nil {
				return err
			}
			go schedule.Run(ctx)
		}
	}

	if p.config.Backup.SettingsOnChange {
		p.cache.EnableBackupOnChange(p.config.Paths.BackupDir, p.config.Backup.KeepSettings, p.logger)
	}
	return nil
}
