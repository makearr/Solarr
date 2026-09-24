package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// MaintenanceReport is what one maintenance pass did to one database.
type MaintenanceReport struct {
	Database   string
	Checkpoint CheckpointReport
	// SizeBefore and SizeAfter are the database and its log together, so the log can say what was freed.
	SizeBefore int64
	SizeAfter  int64
	Duration   time.Duration
}

// Maintain checks, optimises, vacuums and checkpoints while holding the writer, so writes wait;
// a corrupt database stops before VACUUM, which would rebuild it from whatever it can still read.
func (s *Store) Maintain(ctx context.Context) (MaintenanceReport, error) {
	report := MaintenanceReport{Database: s.config.Name, SizeBefore: s.fileSize()}
	started := s.clock.Now()
	err := s.writeExclusive(ctx, PrioBulk, true, func(ctx context.Context, db *sql.DB) error {
		if err := checkOn(ctx, db, s.config.Name, true); err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx, "PRAGMA optimize"); err != nil {
			return fmt.Errorf("store %s: optimize: %w", s.config.Name, err)
		}
		if _, err := db.ExecContext(ctx, "VACUUM"); err != nil {
			return fmt.Errorf("store %s: vacuum: %w", s.config.Name, err)
		}
		// In WAL mode VACUUM's rewrite sits in the log; only this checkpoint shrinks the file.
		return s.truncateTheLog(ctx, db, &report.Checkpoint)
	})
	report.Duration = s.clock.Since(started)
	report.SizeAfter = s.fileSize()
	return report, err
}

// truncateTheLog moves every frame into the database and empties the log file, holding the
// checkpointer lock so the background checkpointer does not contend with it.
func (s *Store) truncateTheLog(ctx context.Context, db *sql.DB, report *CheckpointReport) error {
	s.checkpointMu.Lock()
	defer s.checkpointMu.Unlock()

	var blocked int
	err := db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").
		Scan(&blocked, &report.WALPages, &report.CheckpointedPages)
	if err != nil {
		return fmt.Errorf("store %s: checkpoint: %w", s.config.Name, err)
	}
	report.Blocked = blocked != 0
	return nil
}

func (s *Store) fileSize() int64 {
	var total int64
	for _, path := range []string{s.config.Path, s.config.Path + "-wal"} {
		if info, err := s.fs.Stat(path); err == nil {
			total += info.Size
		}
	}
	return total
}
