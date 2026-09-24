package app

import (
	"log/slog"

	"github.com/makearr/Solarr/internal/platform/config"
)

// startBackupSchedules keeps settings.db's backup on every change (settings_on_change -- it is
// tiny and precious, ADR-25). Routine backups and their cleanup are scheduler tasks since
// 2026-09-24, so config.yml's backup.schedule and backup.keep are read no longer.
func (p *process) startBackupSchedules() {
	defaults := config.Default().Backup
	if p.config.Backup.Schedule != defaults.Schedule || p.config.Backup.Keep != defaults.Keep {
		p.logger.Warn("config.yml backup.schedule and backup.keep are no longer used; "+
			"set the Backup and Clean old backups tasks on the Scheduler screen instead",
			slog.String("backup.schedule", p.config.Backup.Schedule), slog.Int("backup.keep", p.config.Backup.Keep))
	}
	if p.config.Backup.SettingsOnChange {
		p.cache.EnableBackupOnChange(p.config.Paths.BackupDir, p.config.Backup.KeepSettings, p.logger)
	}
}
