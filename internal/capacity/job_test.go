package capacity_test

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/makearr/MediaNexus/internal/capacity"
	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/fsx"
	"github.com/makearr/MediaNexus/internal/jobs"
	"github.com/makearr/MediaNexus/internal/settings"
	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/migrations"
)

func newCache(t *testing.T) *settings.Cache {
	t.Helper()
	directory := t.TempDir()
	backups := filepath.Join(directory, "backups")
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatalf("create backup directory: %v", err)
	}

	opened, err := store.Open(t.Context(), clock.NewFakeAtEpoch(), store.Config{
		Name: "settings", Path: filepath.Join(directory, "settings.db"),
		Logger: slog.New(slog.DiscardHandler),
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

	cache, err := settings.NewCache(t.Context(), opened)
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	return cache
}

func TestJob_RunRefreshesAWritableRoot(t *testing.T) {
	cache := newCache(t)
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	if err := fsys.MkdirAll("/media/music", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	fsys.SetDiskUsage(2_000_000_000_000, 800_000_000_000)

	root, err := cache.CreateRootFolder(t.Context(), "/media/music", "")
	if err != nil {
		t.Fatalf("CreateRootFolder: %v", err)
	}

	job := capacity.NewJob(capacity.Config{
		FS: fsys, Cache: cache, Clock: clock.NewFakeAtEpoch(), Logger: slog.New(slog.DiscardHandler),
	})
	if err := job.Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	updated, found := cache.Snapshot().RootFolder(root.ID)
	if !found {
		t.Fatal("RootFolder not found after Run")
	}
	if !updated.Writable {
		t.Errorf("Writable = false, want true (the probe file wrote and removed cleanly)")
	}
	if updated.TotalBytes == nil || *updated.TotalBytes != 2_000_000_000_000 {
		t.Errorf("TotalBytes = %v, want 2_000_000_000_000", updated.TotalBytes)
	}
	if updated.FreeBytes == nil || *updated.FreeBytes != 800_000_000_000 {
		t.Errorf("FreeBytes = %v, want 800_000_000_000", updated.FreeBytes)
	}
	if updated.CapacityCheckedAt == nil {
		t.Error("CapacityCheckedAt is nil, want it set")
	}
}

func TestJob_RunReportsAnUnwritableRootRatherThanFailingTheSweep(t *testing.T) {
	cache := newCache(t)
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	// /media/music is never created, so the write probe fails -- the same
	// shape an unmounted or permission-denied root produces for real.

	root, err := cache.CreateRootFolder(t.Context(), "/media/music", "")
	if err != nil {
		t.Fatalf("CreateRootFolder: %v", err)
	}

	job := capacity.NewJob(capacity.Config{
		FS: fsys, Cache: cache, Clock: clock.NewFakeAtEpoch(), Logger: slog.New(slog.DiscardHandler),
	})
	if err := job.Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	updated, found := cache.Snapshot().RootFolder(root.ID)
	if !found {
		t.Fatal("RootFolder not found after Run")
	}
	if updated.Writable {
		t.Error("Writable = true, want false (the directory does not exist)")
	}
	if updated.WritableReason == "" {
		t.Error("WritableReason is empty, want a reason recorded")
	}
}

func TestJob_RunContinuesPastOneFailedRootToRefreshTheRest(t *testing.T) {
	cache := newCache(t)
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	if err := fsys.MkdirAll("/media/good", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// /media/bad is never created.

	bad, err := cache.CreateRootFolder(t.Context(), "/media/bad", "")
	if err != nil {
		t.Fatalf("CreateRootFolder: %v", err)
	}
	good, err := cache.CreateRootFolder(t.Context(), "/media/good", "")
	if err != nil {
		t.Fatalf("CreateRootFolder: %v", err)
	}

	job := capacity.NewJob(capacity.Config{
		FS: fsys, Cache: cache, Clock: clock.NewFakeAtEpoch(), Logger: slog.New(slog.DiscardHandler),
	})
	if err := job.Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	updatedBad, _ := cache.Snapshot().RootFolder(bad.ID)
	if updatedBad.Writable {
		t.Error("bad root: Writable = true, want false")
	}
	updatedGood, _ := cache.Snapshot().RootFolder(good.ID)
	if !updatedGood.Writable {
		t.Error("good root: Writable = false, want true -- one bad root must not block the rest of the sweep")
	}
}
