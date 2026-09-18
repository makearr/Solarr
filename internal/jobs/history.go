package jobs

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/jobsdb"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// failBatchSize bounds each transaction against Invariant 2.
const failBatchSize = 200

// Event is one durable history entry (section 5.7). Unlike the event bus, which is
// lossy and for notification only (ADR-9), this survives a restart.
type Event struct {
	Kind          string
	SubjectType   string
	SubjectID     int64
	UserID        int64
	CorrelationID string
	// Data is the entry's payload, already encoded.
	Data string
}

// History appends to and is the durable record in jobs.db, which is where the churn
// it shares lives (ADR-25 keeps settings.db small).
type History struct {
	store *store.Store
	clock clock.Clock
}

// NewHistory builds it over an open jobs store.
func NewHistory(opened *store.Store, cl clock.Clock) History {
	return History{store: opened, clock: cl}
}

// Append records one event and returns its id.
func (h History) Append(ctx context.Context, event Event) (int64, error) {
	var id int64
	err := h.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		var appendErr error
		id, appendErr = jobsdb.New(tx).AppendEvent(ctx, jobsdb.AppendEventParams{
			At:            h.clock.Now().Unix(),
			Kind:          event.Kind,
			SubjectType:   nullableText(event.SubjectType),
			SubjectID:     nullableID(event.SubjectID),
			UserID:        nullableID(event.UserID),
			CorrelationID: nullableText(event.CorrelationID),
			DataJson:      event.Data,
		})
		return appendErr
	})
	if err != nil {
		return 0, fmt.Errorf("jobs: append %s event: %w", event.Kind, err)
	}
	return id, nil
}

// FailUnfinished fails every job that has not reached a terminal state, in batches.
// It is a recovery operation: it runs before workers start, so nothing it fails is
// running (ADR-15).
func (q *Queue) FailUnfinished(ctx context.Context, reason string) (int64, error) {
	now := q.clock.Now().Unix()
	var total int64
	for {
		var changed int64
		err := q.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			var batchErr error
			changed, batchErr = jobsdb.New(tx).FailUnfinishedJobsBatch(ctx, jobsdb.FailUnfinishedJobsBatchParams{
				Reason:    sql.NullString{String: reason, Valid: true},
				Now:       now,
				BatchSize: failBatchSize,
			})
			return batchErr
		})
		if err != nil {
			return total, fmt.Errorf("jobs: fail unfinished jobs: %w", err)
		}
		// A failed job leaves the set this matches, so an empty batch means done.
		if changed == 0 {
			return total, nil
		}
		total += changed
	}
}

func nullableText(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}

func nullableID(value int64) sql.NullInt64 {
	return sql.NullInt64{Int64: value, Valid: value != 0}
}
