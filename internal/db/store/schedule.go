package store

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/robfig/cron/v3"

	"github.com/makearr/Solarr/internal/platform/clock"
)

// cronParser matches internal/platform/config's validator, so a schedule accepted at startup
// is guaranteed to parse here too.
var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// BackupSchedule drives one store's periodic backup and rotation (ADR-15) on a cron
// expression, using the injected clock (ADR-17) rather than time.Now/time.AfterFunc.
type BackupSchedule struct {
	store    *Store
	schedule cron.Schedule
	clock    clock.Clock
	dir      string
	keep     int
	logger   *slog.Logger
}

// NewBackupSchedule parses expr -- a five-field cron expression, already validated at
// config load (internal/platform/config's Backup.validate uses the identical parser) -- and
// builds a schedule for one store.
func NewBackupSchedule(
	opened *Store, expr, dir string, keep int, cl clock.Clock, logger *slog.Logger,
) (*BackupSchedule, error) {
	parsed, err := cronParser.Parse(expr)
	if err != nil {
		return nil, fmt.Errorf("store %s: backup schedule %q: %w", opened.config.Name, expr, err)
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &BackupSchedule{
		store: opened, schedule: parsed, clock: cl, dir: dir, keep: keep, logger: logger,
	}, nil
}

// Run drives the loop until ctx ends. Each fire backs up, then prunes to Keep;
// either failing is logged, not fatal -- a missed backup must not take the server
// down, and the next scheduled fire tries again.
func (b *BackupSchedule) Run(ctx context.Context) {
	for {
		next := b.schedule.Next(b.clock.Now())
		if err := b.clock.Sleep(ctx, b.clock.Until(next)); err != nil {
			return
		}
		b.fire(ctx)
	}
}

func (b *BackupSchedule) fire(ctx context.Context) {
	path, err := b.store.Backup(ctx, b.dir, BackupRotation)
	if err != nil {
		b.logger.Error("scheduled backup failed",
			slog.String("database", b.store.config.Name), slog.Any("error", err))
		return
	}
	b.logger.Info("scheduled backup",
		slog.String("database", b.store.config.Name), slog.String("path", path))

	if _, err := b.store.PruneBackups(b.dir, b.keep); err != nil {
		b.logger.Error("backup rotation failed",
			slog.String("database", b.store.config.Name), slog.Any("error", err))
	}
}
