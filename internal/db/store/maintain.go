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
		if err := optimizeAll(ctx, db); err != nil {
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

// Analyze refreshes the planner statistics on the writer. Plain PRAGMA optimize only looks at
// tables the writer's own queries touched, which left a 9,000-row table at "1 row" and its joins as scans.
func (s *Store) Analyze(ctx context.Context) error {
	err := s.writeExclusive(ctx, PrioBulk, true, func(ctx context.Context, db *sql.DB) error {
		if err := optimizeAll(ctx, db); err != nil {
			return fmt.Errorf("store %s: analyze: %w", s.config.Name, err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	// Another connection keeps the statistics it loaded with its schema; closing the idle ones makes them reload.
	s.read.SetMaxIdleConns(0)
	s.read.SetMaxIdleConns(s.config.ReadPoolSize)
	return nil
}

// optimizeAll is PRAGMA optimize with 0x10000 (every table, not only those this connection queried)
// and 0x2 (run ANALYZE where needed); the analysis limit keeps a large file to about a tenth of a second.
func optimizeAll(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, "PRAGMA analysis_limit=1000"); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, "PRAGMA optimize(0x10002)")
	return err
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
