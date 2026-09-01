package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// TransactionReport describes a transaction that ran longer than its budget.
// Invariant 2 says no write transaction exceeds its bounded work unit; this is
// how that is measured rather than merely asserted (§4.2).
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

// Write submits a bounded unit of work to the priority queue and waits for it to
// commit. The transaction is created with BEGIN IMMEDIATE, so contention is a
// clean wait at the start rather than a failure partway through (§4.1).
//
// It waits for the result even when ctx is cancelled. Returning early would let
// a transaction the caller believes failed go on to commit; instead ctx is
// handed to the transaction, which is bounded by Invariant 2 anyway, and an
// operation still queued when its context ends is dropped without running.
func (s *Store) Write(ctx context.Context, priority Priority, run func(context.Context, *sql.Tx) error) error {
	op := newWriteOp(ctx, priority, s.clock.Now(), run)
	if err := s.queue.push(op); err != nil {
		return fmt.Errorf("store %s: %w", s.config.Name, err)
	}

	select {
	case err := <-op.result:
		return err
	case <-s.writerDone:
		// Unreachable while the queue drains before the writer exits, which it
		// does and which is tested. It is here because the alternative failure
		// mode is a caller blocked forever: mutating the drain away turns every
		// affected test from a failure into a hang, and it would do the same to
		// a running process.
		select {
		case err := <-op.result:
			return err
		default:
			return fmt.Errorf("store %s: %w before the write ran", s.config.Name, ErrClosed)
		}
	}
}

// WriteBatched chunks items and submits each chunk as its own queued
// transaction, so interactive writes interleave between chunks (§4.2). Every
// bulk path uses it: a single transaction over 500k rows would hold the writer
// for minutes and violate Invariant 2 by construction.
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
	}
}

func (s *Store) execute(op *writeOp) error {
	started := s.clock.Now()
	queueWait := started.Sub(op.enqueued)

	// An operation whose caller has already given up never reaches the database:
	// BeginTx rejects a cancelled context, and database/sql rolls a transaction
	// back if its context ends after it opened. An explicit pre-check here was
	// removed after mutation testing showed it changed nothing.
	tx, err := s.write.BeginTx(op.ctx, nil)
	if err != nil {
		return fmt.Errorf("store %s: begin: %w", s.config.Name, err)
	}

	if err := op.run(op.ctx, tx); err != nil {
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			// Both are reported: the caller needs its own error, and a failed
			// rollback is a database problem the operation error would hide.
			return errors.Join(err, fmt.Errorf("store %s: rollback: %w", s.config.Name, rollbackErr))
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store %s: commit: %w", s.config.Name, err)
	}

	s.reportDuration(op, s.clock.Since(started), queueWait)
	return nil
}

// reportDuration enforces the transaction budget. A rule nobody measures is a
// rule nobody keeps, so exceeding the hard limit is a test failure when a
// reporter is wired, and a logged error with a metric in production — never an
// aborted user operation, because correctness outranks speed (ADR-28).
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
			slog.String("database", report.Database),
			slog.String("priority", report.Priority.String()),
			slog.Duration("duration", report.Duration),
			slog.Duration("budget", report.Budget),
			slog.Duration("queue_wait", report.QueueWait))
		return
	}

	report.Budget = s.config.WriteHard
	s.logger.Error("write transaction over its hard budget (Invariant 2)",
		slog.String("database", report.Database),
		slog.String("priority", report.Priority.String()),
		slog.Duration("duration", report.Duration),
		slog.Duration("budget", report.Budget),
		slog.Duration("queue_wait", report.QueueWait))

	if s.config.OnBudgetExceeded != nil {
		s.config.OnBudgetExceeded(report)
	}
}
