// Package statisticstest opens a throwaway statistics.db for a test, migrated and closed with it.
package statisticstest

import (
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/migrations"
)

// Open answers an empty, migrated statistics store that is closed when the test ends.
func Open(t testing.TB) *store.Store {
	t.Helper()
	directory := t.TempDir()
	backups := filepath.Join(directory, "backups")
	if err := fsx.NewOS().MkdirAll(backups, 0o755); err != nil {
		t.Fatalf("statistics backups: %v", err)
	}
	opened, err := store.Open(t.Context(), clock.NewFakeAtEpoch(), store.Config{
		Name: "statistics", Path: filepath.Join(directory, "statistics.db"), Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("open statistics: %v", err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	owner, err := migrations.Statistics()
	if err != nil {
		t.Fatalf("statistics migrations: %v", err)
	}
	if err := store.Migrate(t.Context(), opened, backups, owner); err != nil {
		t.Fatalf("migrate statistics: %v", err)
	}
	return opened
}
