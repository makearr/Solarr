package recovery_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/recovery"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/jobsdb"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/migrations"
)

type fixture struct {
	library *store.Store
	jobs    *store.Store
	clock   *clock.Fake
	queue   *jobs.Queue
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	directory := t.TempDir()
	backups := filepath.Join(directory, "backups")
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatalf("create backup directory: %v", err)
	}
	fake := clock.NewFakeAtEpoch()

	open := func(name string, owner func() (store.Owner, error)) *store.Store {
		opened, err := store.Open(t.Context(), fake, store.Config{
			Name: name, Path: filepath.Join(directory, name+".db"),
			Logger: slog.New(slog.DiscardHandler),
		})
		if err != nil {
			t.Fatalf("Open %s: %v", name, err)
		}
		t.Cleanup(func() { _ = opened.Close() })
		loaded, err := owner()
		if err != nil {
			t.Fatalf("migrations for %s: %v", name, err)
		}
		if err := store.Migrate(t.Context(), opened, backups, loaded); err != nil {
			t.Fatalf("Migrate %s: %v", name, err)
		}
		return opened
	}

	registry := jobs.NewRegistry()
	registry.MustRegister(jobs.Definition{Type: "import.run", Pool: jobs.PoolImport, Mutating: true})
	jobsStore := open("jobs", migrations.Jobs)

	return fixture{
		library: open("library", migrations.Library),
		jobs:    jobsStore,
		clock:   fake,
		queue:   jobs.NewQueue(jobsStore, fake, registry, jobs.NoJitter),
	}
}

// desiredItem creates one already-planned item, which is the state a restore has to
// undo -- a dirty item would be re-planned whether reconciliation ran or not.
func (f fixture) desiredItem(t *testing.T, categoryID int64) {
	t.Helper()
	err := f.library.Write(t.Context(), store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(f.library.Read()).WithTx(tx)
		work, err := queries.CreateWork(ctx, librarydb.CreateWorkParams{
			MediaType: "music", CanonicalTitle: "Tender Prey", SortTitle: "Tender Prey",
			AddedAt: 1, UpdatedAt: 1,
		})
		if err != nil {
			return err
		}
		item, err := queries.CreateDesiredItem(ctx, librarydb.CreateDesiredItemParams{
			WorkID: work.ID, CategoryID: categoryID, ProfileID: 1, Monitored: 1,
			Dirty: 1, SatisfactionKind: "SINGLE", State: "MISSING",
			RequiredCount: sql.NullInt64{Int64: 1, Valid: true},
		})
		if err != nil {
			return err
		}
		return queries.MarkDesiredItemPlanned(ctx, librarydb.MarkDesiredItemPlannedParams{
			LastPlannedAt: sql.NullInt64{Int64: 1, Valid: true},
			ID:            item.ID, Generation: item.Generation,
		})
	})
	if err != nil {
		t.Fatalf("create desired item: %v", err)
	}
}

func (f fixture) recorded(t *testing.T) recovery.Report {
	t.Helper()
	found, err := jobsdb.New(f.jobs.Read()).ListEventsByKind(t.Context(), jobsdb.ListEventsByKindParams{
		Kind: recovery.EventKind, BeforeID: 0, PageSize: 10,
	})
	if err != nil {
		t.Fatalf("ListEventsByKind: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("found %d restore events, want exactly 1", len(found))
	}
	var decoded recovery.Report
	if err := json.Unmarshal([]byte(found[0].DataJson), &decoded); err != nil {
		t.Fatalf("decode the recorded report: %v", err)
	}
	return decoded
}

func TestReconcile_FailsTheQueueDirtiesTheLibraryAndRecordsWhy(t *testing.T) {
	f := newFixture(t)
	f.desiredItem(t, 1)
	f.desiredItem(t, 1)
	if _, err := f.queue.Enqueue(t.Context(), jobs.Request{Type: "import.run"}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	takenAt := f.clock.Now()
	f.clock.Advance(90 * time.Minute)
	report, err := recovery.Reconcile(t.Context(), f.clock, f.library, f.jobs,
		recovery.Known{CategoryIDs: []int64{1}},
		[]store.RecoveryReport{{
			Database: "library", Recovered: true, BackupAt: takenAt,
			From: "/backups/library.db.rotation.x", CorruptPath: "/db/library.db.corrupt.x",
		}})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if report.JobsFailed != 1 {
		t.Errorf("JobsFailed = %d, want 1", report.JobsFailed)
	}
	if report.DesiredItemsDirtied != 2 {
		t.Errorf("DesiredItemsDirtied = %d, want 2", report.DesiredItemsDirtied)
	}
	if len(report.Orphans) != 0 {
		t.Errorf("Orphans = %+v, want none: category 1 is configured", report.Orphans)
	}
	// ADR-15 requires the notification to say how much history was lost.
	if len(report.Restored) != 1 {
		t.Fatalf("Restored = %+v, want one database", report.Restored)
	}
	if report.Restored[0].HistoryLostSeconds != int64((90 * time.Minute).Seconds()) {
		t.Errorf("HistoryLostSeconds = %d, want 5400", report.Restored[0].HistoryLostSeconds)
	}
	if len(report.Gaps) == 0 {
		t.Error("Gaps is empty: what ADR-15 asks for and this cannot do yet must be named")
	}

	// The report has to survive the restart that produced it, so what matters is
	// what came back out of the event log, not what was returned.
	stored := f.recorded(t)
	if stored.JobsFailed != report.JobsFailed || len(stored.Restored) != 1 {
		t.Fatalf("recorded %+v, want it to match the returned %+v", stored, report)
	}
	if stored.Restored[0].CorruptPath != "/db/library.db.corrupt.x" {
		t.Errorf("recorded CorruptPath = %q, want the recycled path", stored.Restored[0].CorruptPath)
	}
}

func TestReconcile_ReportsAReferenceTheRestoreOrphaned(t *testing.T) {
	f := newFixture(t)
	f.desiredItem(t, 1)
	f.desiredItem(t, 7)
	f.desiredItem(t, 7)

	// settings.db knows category 1 only, which is what restoring one database and
	// not the other looks like (ADR-25).
	report, err := recovery.Reconcile(t.Context(), f.clock, f.library, f.jobs,
		recovery.Known{CategoryIDs: []int64{1}},
		[]store.RecoveryReport{{Database: "library", Recovered: true}})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	want := []recovery.OrphanedReference{
		{Table: "desired_item", Column: "category_id", ID: 7, Rows: 2},
	}
	if len(report.Orphans) != 1 || report.Orphans[0] != want[0] {
		t.Fatalf("Orphans = %+v, want %+v", report.Orphans, want)
	}
	if stored := f.recorded(t); len(stored.Orphans) != 1 || stored.Orphans[0] != want[0] {
		t.Errorf("recorded Orphans = %+v, want %+v", stored.Orphans, want)
	}
}

func TestReconcile_OnAnEmptyInstallStillRecordsTheRestore(t *testing.T) {
	f := newFixture(t)

	report, err := recovery.Reconcile(t.Context(), f.clock, f.library, f.jobs,
		recovery.Known{},
		[]store.RecoveryReport{{Database: "library", Recovered: true, From: "/backups/one"}})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if report.JobsFailed != 0 || report.DesiredItemsDirtied != 0 || len(report.Orphans) != 0 {
		t.Errorf("report = %+v, want nothing found", report)
	}
	stored := f.recorded(t)
	if len(stored.Restored) != 1 || stored.Restored[0].Backup != "/backups/one" {
		t.Errorf("recorded Restored = %+v, want the one backup", stored.Restored)
	}
}
