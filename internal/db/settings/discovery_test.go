package settings_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/settingsdb"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/migrations"
)

// newSettingsStore opens settings.db without the Cache wrapper: discovery_job is not
// part of the in-memory snapshot yet (ADR-38's execution logic is stage 12; the
// sibling policy tables the same architecture section sketches -- indexer,
// downloader, metadata_provider -- do not exist as tables yet either), so its
// queries are exercised directly, the way internal/db/library exercises librarydb.
func newSettingsStore(t *testing.T) (*store.Store, *settingsdb.Queries) {
	t.Helper()
	directory := t.TempDir()
	backups := filepath.Join(directory, "backups")
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatalf("create backup directory: %v", err)
	}

	opened, err := store.Open(t.Context(), clock.NewFakeAtEpoch(), store.Config{
		Name: "settings", Path: filepath.Join(directory, "settings.db"),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = opened.Close() })

	owner, err := migrations.Settings()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	if err := store.Migrate(t.Context(), opened, backups, owner); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return opened, settingsdb.New(opened.Read())
}

func writeSettings(t *testing.T, opened *store.Store, mutate func(context.Context, *settingsdb.Queries) error) {
	t.Helper()
	err := opened.Write(t.Context(), store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		return mutate(ctx, settingsdb.New(opened.Read()).WithTx(tx))
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
}

func createDiscoveryJob(t *testing.T, opened *store.Store, name string) settingsdb.DiscoveryJob {
	t.Helper()
	var job settingsdb.DiscoveryJob
	writeSettings(t, opened, func(ctx context.Context, q *settingsdb.Queries) error {
		row, err := q.CreateDiscoveryJob(ctx, settingsdb.CreateDiscoveryJobParams{
			Name: name, SourceJson: `{"kind":"list","provider":"trakt","list":"trending"}`,
			FiltersJson: `{}`, LimitPerRun: 10, Action: "request",
		})
		job = row
		return err
	})
	return job
}

// ADR-38's central safety feature: a job is off until someone previews it and turns
// it on -- never enabled just by existing.
func TestDiscoveryJob_IsDisabledByDefault(t *testing.T) {
	opened, _ := newSettingsStore(t)
	job := createDiscoveryJob(t, opened, "quality-films")

	if job.Enabled != 0 {
		t.Errorf("Enabled = %d, want 0 (off until previewed)", job.Enabled)
	}
}

func TestDiscoveryJob_ListEnabledOnlyReturnsEnabledJobs(t *testing.T) {
	opened, queries := newSettingsStore(t)
	off := createDiscoveryJob(t, opened, "off-job")
	on := createDiscoveryJob(t, opened, "on-job")
	_ = off

	writeSettings(t, opened, func(ctx context.Context, q *settingsdb.Queries) error {
		return q.SetDiscoveryJobEnabled(ctx, settingsdb.SetDiscoveryJobEnabledParams{Enabled: 1, ID: on.ID})
	})

	enabled, err := queries.ListEnabledDiscoveryJobs(t.Context())
	if err != nil {
		t.Fatalf("ListEnabledDiscoveryJobs: %v", err)
	}
	if len(enabled) != 1 || enabled[0].ID != on.ID {
		t.Errorf("got %+v, want only %q enabled", enabled, "on-job")
	}
}

func TestDiscoveryJob_RecordRunSetsLastRunAt(t *testing.T) {
	opened, queries := newSettingsStore(t)
	job := createDiscoveryJob(t, opened, "quality-films")

	writeSettings(t, opened, func(ctx context.Context, q *settingsdb.Queries) error {
		return q.RecordDiscoveryJobRun(ctx, settingsdb.RecordDiscoveryJobRunParams{
			LastRunAt: sql.NullInt64{Int64: 42, Valid: true}, ID: job.ID,
		})
	})

	found, err := queries.GetDiscoveryJob(t.Context(), job.ID)
	if err != nil {
		t.Fatalf("GetDiscoveryJob: %v", err)
	}
	if found.LastRunAt.Int64 != 42 {
		t.Errorf("LastRunAt = %v, want 42", found.LastRunAt)
	}
}

// Invariant 41: deleting a category a discovery job still points at is refused with
// a reason, not a raw foreign-key error.
func TestDiscoveryJob_BlocksDeletingItsCategory(t *testing.T) {
	opened, _ := newSettingsStore(t)
	cache, err := settings.NewCache(t.Context(), opened)
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	category, err := cache.CreateCategory(t.Context(), "movie", "Movies", 10)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}

	writeSettings(t, opened, func(ctx context.Context, q *settingsdb.Queries) error {
		_, err := q.CreateDiscoveryJob(ctx, settingsdb.CreateDiscoveryJobParams{
			Name: "quality-films", SourceJson: `{}`, FiltersJson: `{}`, LimitPerRun: 10,
			Action:     "request",
			CategoryID: sql.NullInt64{Int64: category.ID, Valid: true},
		})
		return err
	})

	err = cache.DeleteCategory(t.Context(), category.ID)
	if err == nil {
		t.Fatal("DeleteCategory succeeded, want it refused: a discovery job still uses this category")
	}
}
