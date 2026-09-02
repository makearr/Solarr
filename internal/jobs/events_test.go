package jobs_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/internal/store/gen/jobsdb"
	"github.com/makearr/MediaNexus/migrations"
)

// newEventStore opens jobs.db directly: the event log is beside the queue in that file
// but has nothing to do with it (section 5.7).
func newEventStore(t *testing.T) (*store.Store, *jobsdb.Queries) {
	t.Helper()
	directory := t.TempDir()
	backups := filepath.Join(directory, "backups")
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatalf("create backup directory: %v", err)
	}

	opened, err := store.Open(t.Context(), clock.NewFakeAtEpoch(), store.Config{
		Name: "jobs", Path: filepath.Join(directory, "jobs.db"), Logger: quietLogger(),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = opened.Close() })

	owner, err := migrations.Jobs()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	if err := store.Migrate(t.Context(), opened, backups, owner); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return opened, jobsdb.New(opened.Read())
}

func appendEvent(t *testing.T, opened *store.Store, at int64, kind, subjectType string, subjectID int64) int64 {
	t.Helper()
	var id int64
	err := opened.Write(t.Context(), store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		created, err := jobsdb.New(opened.Read()).WithTx(tx).AppendEvent(ctx, jobsdb.AppendEventParams{
			At: at, Kind: kind,
			SubjectType: sql.NullString{String: subjectType, Valid: subjectType != ""},
			SubjectID:   sql.NullInt64{Int64: subjectID, Valid: subjectID != 0},
			DataJson:    `{}`,
		})
		id = created
		return err
	})
	if err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	return id
}

// "What happened to this album" is the first question the history view asks, and it must
// not read another album's events on the way to answering it.
func TestEventLog_ReadsBackOneSubjectNewestFirst(t *testing.T) {
	opened, _ := newEventStore(t)
	queries := jobsdb.New(opened.Read())

	first := appendEvent(t, opened, 100, "ImportCompleted", "work", 7)
	second := appendEvent(t, opened, 200, "DecisionMade", "work", 7)
	appendEvent(t, opened, 300, "ImportCompleted", "work", 8)

	rows, err := queries.ListEventsForSubject(t.Context(), jobsdb.ListEventsForSubjectParams{
		SubjectType: sql.NullString{String: "work", Valid: true},
		SubjectID:   sql.NullInt64{Int64: 7, Valid: true},
		BeforeID:    0, PageSize: 10,
	})
	if err != nil {
		t.Fatalf("ListEventsForSubject: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d events for work 7, want 2", len(rows))
	}
	if rows[0].ID != second || rows[1].ID != first {
		t.Errorf("got ids %d, %d; want %d then %d, newest first", rows[0].ID, rows[1].ID, second, first)
	}
}

// Two events in the same millisecond are ordinary during an import batch, so the cursor
// pages on the id. On `at` alone a page boundary inside one instant drops rows.
func TestEventLog_PagesThroughEventsSharingOneInstant(t *testing.T) {
	opened, _ := newEventStore(t)
	queries := jobsdb.New(opened.Read())

	const total = 25
	for range total {
		appendEvent(t, opened, 1, "ImportCompleted", "work", 7)
	}

	seen := map[int64]int{}
	before := int64(0)
	for range total {
		rows, err := queries.ListEventsForSubject(t.Context(), jobsdb.ListEventsForSubjectParams{
			SubjectType: sql.NullString{String: "work", Valid: true},
			SubjectID:   sql.NullInt64{Int64: 7, Valid: true},
			BeforeID:    before, PageSize: 10,
		})
		if err != nil {
			t.Fatalf("ListEventsForSubject: %v", err)
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			seen[row.ID]++
		}
		before = rows[len(rows)-1].ID
	}

	if len(seen) != total {
		t.Errorf("walked %d of %d events", len(seen), total)
	}
	for id, count := range seen {
		if count != 1 {
			t.Errorf("event %d appeared %d times", id, count)
		}
	}
}

// Retention must not become one enormous transaction (Invariant 2), so a pass is bounded
// and resumable rather than deleting everything it finds.
func TestEventLog_PruningIsBoundedPerPass(t *testing.T) {
	opened, _ := newEventStore(t)

	for index := range 50 {
		appendEvent(t, opened, int64(index), "ImportCompleted", "work", 7)
	}

	var removed int64
	err := opened.Write(t.Context(), store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		count, err := jobsdb.New(opened.Read()).WithTx(tx).PruneEventsBefore(ctx,
			jobsdb.PruneEventsBeforeParams{Before: 100, BatchSize: 20})
		removed = count
		return err
	})
	if err != nil {
		t.Fatalf("PruneEventsBefore: %v", err)
	}
	if removed != 20 {
		t.Errorf("pruned %d events in one pass, want the 20 the batch size allows", removed)
	}
}

// Pruning resumes where it stopped, or every pass rescans from the beginning.
func TestRetentionState_RemembersHowFarItGot(t *testing.T) {
	opened, _ := newEventStore(t)
	queries := jobsdb.New(opened.Read())

	set := func(through int64) {
		t.Helper()
		err := opened.Write(t.Context(), store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			return jobsdb.New(opened.Read()).WithTx(tx).SetRetentionState(ctx,
				jobsdb.SetRetentionStateParams{Stream: "event_log", PrunedThrough: through})
		})
		if err != nil {
			t.Fatalf("SetRetentionState: %v", err)
		}
	}

	set(100)
	set(250)

	state, err := queries.GetRetentionState(t.Context(), "event_log")
	if err != nil {
		t.Fatalf("GetRetentionState: %v", err)
	}
	if state.PrunedThrough != 250 {
		t.Errorf("pruned_through = %d, want 250: the second pass must replace the first",
			state.PrunedThrough)
	}
}
