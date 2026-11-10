package statistics

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/statisticsdb"
)

// TaskHour is the time one kind of job ran in one hour, and how many runs made it.
type TaskHour struct {
	JobType   string
	HourStart time.Time
	RunMs     int64
	Runs      int64
}

type taskHourKey struct {
	jobType string
	hour    int64
}

type taskHourSum struct{ ms, runs int64 }

// writeChunk bounds the rows one flush writes in a transaction.
const writeChunk = 200

// TaskHours logs what the workers did, hour by hour. Runs are added in memory and written by Flush, so a burst of
// short jobs is a few writes rather than one each.
type TaskHours struct {
	store *store.Store

	mu      sync.Mutex
	pending map[taskHourKey]taskHourSum
}

// NewTaskHours builds it over an open statistics store.
func NewTaskHours(opened *store.Store) *TaskHours {
	return &TaskHours{store: opened, pending: map[taskHourKey]taskHourSum{}}
}

// RecordRun adds a finished run, split over the hours it spanned; the run is counted in the hour it began.
func (t *TaskHours) RecordRun(jobType string, start, end time.Time) {
	if !end.After(start) {
		end = start.Add(time.Millisecond)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for cursor := start; cursor.Before(end); {
		hour := cursor.UTC().Truncate(time.Hour)
		next := hour.Add(time.Hour)
		if next.After(end) {
			next = end
		}
		key := taskHourKey{jobType, hour.Unix()}
		sum := t.pending[key]
		sum.ms += next.Sub(cursor).Milliseconds()
		if cursor.Equal(start) {
			sum.runs++
		}
		t.pending[key] = sum
		cursor = next
	}
}

// Flush writes what has been added since the last flush; a failed write keeps the rows for the next one.
func (t *TaskHours) Flush(ctx context.Context) error {
	t.mu.Lock()
	taken := t.pending
	t.pending = map[taskHourKey]taskHourSum{}
	t.mu.Unlock()
	if len(taken) == 0 {
		return nil
	}
	keys := make([]taskHourKey, 0, len(taken))
	for key := range taken {
		keys = append(keys, key)
	}
	for start := 0; start < len(keys); start += writeChunk {
		chunk := keys[start:min(start+writeChunk, len(keys))]
		err := t.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			queries := statisticsdb.New(tx)
			for _, key := range chunk {
				if err := queries.AddTaskHour(ctx, statisticsdb.AddTaskHourParams{
					JobType: key.jobType, HourStart: key.hour, RunMs: taken[key].ms, Runs: taken[key].runs,
				}); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.mu.Lock()
			for _, key := range keys[start:] {
				sum := t.pending[key]
				sum.ms += taken[key].ms
				sum.runs += taken[key].runs
				t.pending[key] = sum
			}
			t.mu.Unlock()
			return fmt.Errorf("statistics: write task hours: %w", err)
		}
	}
	return nil
}

// Since lists the hours from a time on, with what has not been written yet.
func (t *TaskHours) Since(ctx context.Context, since time.Time) ([]TaskHour, error) {
	rows, err := statisticsdb.New(t.store.Read()).ListTaskHoursSince(ctx, since.Unix())
	if err != nil {
		return nil, fmt.Errorf("statistics: list task hours: %w", err)
	}
	merged := map[taskHourKey]taskHourSum{}
	for _, row := range rows {
		merged[taskHourKey{row.JobType, row.HourStart}] = taskHourSum{row.RunMs, row.Runs}
	}
	t.mu.Lock()
	for key, sum := range t.pending {
		if key.hour >= since.Unix() {
			current := merged[key]
			merged[key] = taskHourSum{current.ms + sum.ms, current.runs + sum.runs}
		}
	}
	t.mu.Unlock()
	hours := make([]TaskHour, 0, len(merged))
	for key, sum := range merged {
		hours = append(hours, TaskHour{JobType: key.jobType, HourStart: time.Unix(key.hour, 0).UTC(), RunMs: sum.ms, Runs: sum.runs})
	}
	return hours, nil
}

// Prune removes the hours before a time.
func (t *TaskHours) Prune(ctx context.Context, before time.Time) error {
	err := t.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		_, err := statisticsdb.New(tx).DeleteTaskHoursBefore(ctx, before.Unix())
		return err
	})
	if err != nil {
		return fmt.Errorf("statistics: prune task hours: %w", err)
	}
	return nil
}
