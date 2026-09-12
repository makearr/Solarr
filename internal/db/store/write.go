package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// TransactionReport describes a transaction that ran longer than its budget (Invariant 2,
// §4.2).
type TransactionReport struct {
	Database string
	Priority Priority
	Duration time.Duration
	Budget   time.Duration
	// QueueWait is how long the operation sat before the writer reached it.
	QueueWait time.Duration
}

func (r TransactionReport) String() string {
	return fmt.Sprintf("%s: %s transaction took %s, budget %s (queued %s)",
		r.Database, r.Priority, r.Duration, r.Budget, r.QueueWait)
}

// Write submits a bounded unit of work to the priority queue and waits for it to commit (§4.1,
// Invariant 2).
func (s *Store) Write(ctx context.Context, priority Priority, run func(context.Context, *sql.Tx) error) error {
	op := newWriteOp(ctx, priority, s.clock.Now(), func(ctx context.Context, db *sql.DB) error {
		return runInTransaction(ctx, db, s.config.Name, run)
	})
	if err := s.queue.push(op); err != nil {
		return fmt.Errorf("store %s: %w", s.config.Name, err)
	}

	select {
	case err := <-op.result:
		return err
	case <-s.writerDone:
		// Unreachable while the queue drains before the writer exits, which it does and which is
		// tested.
		select {
		case err := <-op.result:
			return err
		default:
			return fmt.Errorf("store %s: %w before the write ran", s.config.Name, ErrClosed)
		}
	}
}

// WriteBatched chunks items and submits each chunk as its own queued transaction, so
// interactive writes interleave between chunks (§4.2, Invariant 2).
func WriteBatched[T any](
	ctx context.Context,
	s *Store,
	priority Priority,
	size int,
	items []T,
	run func(context.Context, *sql.Tx, []T) error,
) error {
	if size <= 0 {
		return fmt.Errorf("store %s: batch size must be positive, got %d", s.config.Name, size)
	}
	for start := 0; start < len(items); start += size {
		chunk := items[start:min(start+size, len(items))]
		err := s.Write(ctx, priority, func(ctx context.Context, tx *sql.Tx) error {
			return run(ctx, tx, chunk)
		})
		if err != nil {
			return fmt.Errorf("batch at offset %d: %w", start, err)
		}
	}
	return nil
}

// QueueDepth reports how many operations are waiting. Exposed for the
// mnx_write_queue metrics and for tests.
func (s *Store) QueueDepth() int { return s.queue.depth() }

// writeLoop is the single writer goroutine for this database (ADR-3). It exits
// once the queue is closed and drained.
func (s *Store) writeLoop() {
	defer close(s.writerDone)
	for {
		op := s.queue.pop()
		if op == nil {
			return
		}
		op.result <- s.execute(op)
		s.wantCheckpoint()
	}
}

func (s *Store) execute(op *writeOp) error {
	started := s.clock.Now()
	queueWait := started.Sub(op.enqueued)

	if err := op.execute(op.ctx, s.write); err != nil {
		return err
	}
	if !op.exemptFromBudget {
		s.reportDuration(op, s.clock.Since(started), queueWait)
	}
	return nil
}

// runInTransaction is the ordinary write path.
func runInTransaction(ctx context.Context, db *sql.DB, database string, run func(context.Context, *sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store %s: begin: %w", database, err)
	}

	if err := run(ctx, tx); err != nil {
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			// Both are reported: the caller needs its own error, and a failed
			// rollback is a database problem the operation error would hide.
			return errors.Join(err, fmt.Errorf("store %s: rollback: %w", database, rollbackErr))
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store %s: commit: %w", database, err)
	}
	return nil
}

// runWithForeignKeysDisabled runs fn the same way runInTransaction does, but with foreign key
// enforcement suspended around it -- SQLite's own documented procedure for a schema change
// ALTER TABLE cannot express (widening or narrowing a table's own constraints means rebuilding
// it), since dropping a table that other tables still hold live ON DELETE CASCADE/SET NULL
// references into cascades those rows away the moment it is dropped, unless enforcement is off
// for the duration.
//
// PRAGMA foreign_keys is a documented no-op inside a transaction, so it must be toggled on db
// itself before BEGIN and after COMMIT/ROLLBACK, never inside fn. This only works because db here
// is always s.write, pinned to exactly one physical connection (SetMaxOpenConns(1)) -- the pragma
// and the transaction that follows are guaranteed to land on the same connection rather than two
// different ones from a pool, which a general-purpose *sql.DB could not guarantee.
//
// foreign_keys is always re-enabled, success or failure: Invariant 4 requires it, and this is the
// one connection ever allowed to say otherwise, only for the moment this function owns it.
func runWithForeignKeysDisabled(
	ctx context.Context, db *sql.DB, database string, fn func(context.Context, *sql.Tx) error,
) error {
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
		return fmt.Errorf("store %s: disable foreign keys: %w", database, err)
	}

	opErr := runInTransaction(ctx, db, database, func(ctx context.Context, tx *sql.Tx) error {
		if err := fn(ctx, tx); err != nil {
			return err
		}
		// The one safety net foreign_keys=OFF gives up for its duration: refuse to
		// commit a rebuild that left anything referencing a row that no longer exists.
		return checkForeignKeys(ctx, tx, database)
	})

	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys=ON"); err != nil {
		return errors.Join(opErr, fmt.Errorf("store %s: re-enable foreign keys: %w", database, err))
	}
	return opErr
}

// checkForeignKeys runs PRAGMA foreign_key_check and turns any row it reports into an error.
func checkForeignKeys(ctx context.Context, tx *sql.Tx, database string) error {
	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return fmt.Errorf("store %s: foreign key check: %w", database, err)
	}
	defer func() { _ = rows.Close() }()

	var violations []string
	for rows.Next() {
		var table string
		var rowID sql.NullInt64
		var referredTable string
		var foreignKeyIndex int
		if err := rows.Scan(&table, &rowID, &referredTable, &foreignKeyIndex); err != nil {
			return fmt.Errorf("store %s: scan foreign key violation: %w", database, err)
		}
		violations = append(violations, fmt.Sprintf("%s row %v -> missing %s", table, rowID, referredTable))
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("store %s: foreign key check: %w", database, err)
	}
	if len(violations) > 0 {
		return fmt.Errorf("store %s: migration left dangling foreign keys: %s",
			database, strings.Join(violations, ", "))
	}
	return nil
}

// writeExclusive serialises a statement that cannot run inside a transaction,
// through the same queue and therefore the same single writer.
func (s *Store) writeExclusive(ctx context.Context, priority Priority, exemptFromBudget bool,
	run func(context.Context, *sql.DB) error) error {
	op := newWriteOp(ctx, priority, s.clock.Now(), run)
	op.exemptFromBudget = exemptFromBudget
	if err := s.queue.push(op); err != nil {
		return fmt.Errorf("store %s: %w", s.config.Name, err)
	}
	select {
	case err := <-op.result:
		return err
	case <-s.writerDone:
		select {
		case err := <-op.result:
			return err
		default:
			return fmt.Errorf("store %s: %w before the write ran", s.config.Name, ErrClosed)
		}
	}
}

// reportDuration enforces the transaction budget (ADR-28).
func (s *Store) reportDuration(op *writeOp, duration, queueWait time.Duration) {
	if duration < s.config.WriteWarn {
		return
	}

	report := TransactionReport{
		Database:  s.config.Name,
		Priority:  op.priority,
		Duration:  duration,
		Budget:    s.config.WriteWarn,
		QueueWait: queueWait,
	}

	if duration < s.config.WriteHard {
		s.logger.Warn("write transaction over its warning budget",
			slog.String("priority", report.Priority.String()),
			slog.Duration("duration", report.Duration),
			slog.Duration("budget", report.Budget),
			slog.Duration("queue_wait", report.QueueWait))
		return
	}

	report.Budget = s.config.WriteHard
	s.logger.Error("write transaction over its hard budget (Invariant 2)",
		slog.String("priority", report.Priority.String()),
		slog.Duration("duration", report.Duration),
		slog.Duration("budget", report.Budget),
		slog.Duration("queue_wait", report.QueueWait))

	if s.config.OnBudgetExceeded != nil {
		s.config.OnBudgetExceeded(report)
	}
}
