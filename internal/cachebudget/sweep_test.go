package cachebudget_test

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/makearr/MediaNexus/internal/cachebudget"
	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/fsx"
	"github.com/makearr/MediaNexus/internal/metacache"
	"github.com/makearr/MediaNexus/internal/settings"
	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/migrations"
)

const imagesDir = "/cache/images"

type fixture struct {
	sweep    *cachebudget.Sweep
	fs       *fsx.Mem
	metadata metacache.Cache
	settings *settings.Cache
	clock    *clock.Fake
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	fake := clock.NewFakeAtEpoch()
	memFS := fsx.NewMem(fake)

	directory := t.TempDir()
	backups := filepath.Join(directory, "backups")
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatalf("create backup directory: %v", err)
	}

	metadataStore, err := store.Open(t.Context(), fake, store.Config{
		Name: "metadata", Path: filepath.Join(directory, "metadata.db"), Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("Open (metadata): %v", err)
	}
	t.Cleanup(func() { _ = metadataStore.Close() })
	metadataOwner, err := migrations.Metadata()
	if err != nil {
		t.Fatalf("migrations (metadata): %v", err)
	}
	if err := store.Migrate(t.Context(), metadataStore, backups, metadataOwner); err != nil {
		t.Fatalf("Migrate (metadata): %v", err)
	}
	metadataCache := metacache.New(metadataStore)

	settingsStore, err := store.Open(t.Context(), fake, store.Config{
		Name: "settings", Path: filepath.Join(directory, "settings.db"), Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("Open (settings): %v", err)
	}
	t.Cleanup(func() { _ = settingsStore.Close() })
	settingsOwner, err := migrations.Settings()
	if err != nil {
		t.Fatalf("migrations (settings): %v", err)
	}
	if err := store.Migrate(t.Context(), settingsStore, backups, settingsOwner); err != nil {
		t.Fatalf("Migrate (settings): %v", err)
	}
	settingsCache, err := settings.NewCache(t.Context(), settingsStore)
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}

	sweep := cachebudget.NewSweep(cachebudget.Config{
		FS: memFS, ImagesDir: imagesDir, Metadata: metadataCache, Cache: settingsCache,
		Clock: fake, Logger: slog.New(slog.DiscardHandler),
	})
	return fixture{sweep: sweep, fs: memFS, metadata: metadataCache, settings: settingsCache, clock: fake}
}

func (f fixture) writeImage(t *testing.T, name string, size int) {
	t.Helper()
	if err := f.fs.MkdirAll(imagesDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writer, err := f.fs.Create(filepath.Join(imagesDir, name))
	if err != nil {
		t.Fatalf("Create(%s): %v", name, err)
	}
	if _, err := writer.Write(make([]byte, size)); err != nil {
		t.Fatalf("Write(%s): %v", name, err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close(%s): %v", name, err)
	}
}

func TestSweep_MeasureSumsImagesAndMetadata(t *testing.T) {
	f := newFixture(t)
	f.writeImage(t, "a.webp", 100)
	f.writeImage(t, "b.webp", 200)
	if err := f.metadata.Set(t.Context(), "release", "x", metacache.Entry{Body: "12345", FetchedAt: f.clock.Now()}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	report, err := f.sweep.Measure(t.Context())
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}
	if report.Images.Bytes != 300 {
		t.Errorf("Images.Bytes = %d, want 300", report.Images.Bytes)
	}
	if report.Metadata.Bytes != 5 {
		t.Errorf("Metadata.Bytes = %d, want 5", report.Metadata.Bytes)
	}
}

func TestSweep_MeasureTreatsAMissingImagesDirAsEmpty(t *testing.T) {
	f := newFixture(t)
	report, err := f.sweep.Measure(t.Context())
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}
	if report.Images.Bytes != 0 {
		t.Errorf("Images.Bytes = %d, want 0 for a namespace that never wrote anything", report.Images.Bytes)
	}
}

// A generous budget so only max_age's own unconditional pass can explain
// what gets removed.
var generousBudget = settings.CacheBudget{
	MaxBytes: 1_000_000_000, MaxAgeDays: 30, ImagesSharePercent: 80, MetadataSharePercent: 20,
}

func TestSweep_EnforcePrunesImagesOlderThanMaxAge(t *testing.T) {
	f := newFixture(t)
	f.writeImage(t, "old.webp", 10)
	f.clock.Advance(40 * 24 * time.Hour) // now older than the 30-day budget
	f.writeImage(t, "recent.webp", 10)

	result, err := f.sweep.Enforce(t.Context(), generousBudget)
	if err != nil {
		t.Fatalf("Enforce: %v", err)
	}
	if result.ImagesEvicted != 1 {
		t.Errorf("ImagesEvicted = %d, want 1", result.ImagesEvicted)
	}
	if result.Report.Images.Bytes != 10 {
		t.Errorf("Images.Bytes after Enforce = %d, want 10 (only recent.webp left)", result.Report.Images.Bytes)
	}
}

func TestSweep_EnforcePrunesMetadataOlderThanMaxAge(t *testing.T) {
	f := newFixture(t)
	if err := f.metadata.Set(t.Context(), "release", "old", metacache.Entry{Body: "x", FetchedAt: f.clock.Now()}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	f.clock.Advance(40 * 24 * time.Hour)
	if err := f.metadata.Set(t.Context(), "release", "recent", metacache.Entry{Body: "x", FetchedAt: f.clock.Now()}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	result, err := f.sweep.Enforce(t.Context(), generousBudget)
	if err != nil {
		t.Fatalf("Enforce: %v", err)
	}
	if result.MetadataEvicted != 1 {
		t.Errorf("MetadataEvicted = %d, want 1", result.MetadataEvicted)
	}
}

func TestSweep_EnforceEvictsOldestImagesWhenOverBudget(t *testing.T) {
	f := newFixture(t)
	f.writeImage(t, "oldest.webp", 100)
	f.clock.Advance(time.Minute)
	f.writeImage(t, "middle.webp", 100)
	f.clock.Advance(time.Minute)
	f.writeImage(t, "newest.webp", 100)

	// Total 300 bytes; a 150-byte images budget must evict oldest first
	// until at or under it.
	budget := settings.CacheBudget{MaxBytes: 150, MaxAgeDays: 3650, ImagesSharePercent: 100, MetadataSharePercent: 0}
	result, err := f.sweep.Enforce(t.Context(), budget)
	if err != nil {
		t.Fatalf("Enforce: %v", err)
	}
	if result.ImagesEvicted != 2 {
		t.Fatalf("ImagesEvicted = %d, want 2 (oldest and middle)", result.ImagesEvicted)
	}
	if result.Report.Images.Bytes != 100 {
		t.Errorf("Images.Bytes = %d, want 100 (only newest.webp left)", result.Report.Images.Bytes)
	}
}

func TestSweep_EnforceEvictsOldestMetadataWhenOverBudget(t *testing.T) {
	f := newFixture(t)
	for index, id := range []string{"oldest", "middle", "newest"} {
		if err := f.metadata.Set(t.Context(), "release", id, metacache.Entry{Body: "1234567890", FetchedAt: f.clock.Now()}); err != nil {
			t.Fatalf("Set(%s): %v", id, err)
		}
		if index < 2 {
			f.clock.Advance(time.Minute)
		}
	}

	// 30 logical bytes total; a 15-byte metadata budget must evict oldest
	// first.
	budget := settings.CacheBudget{MaxBytes: 15, MaxAgeDays: 3650, ImagesSharePercent: 0, MetadataSharePercent: 100}
	result, err := f.sweep.Enforce(t.Context(), budget)
	if err != nil {
		t.Fatalf("Enforce: %v", err)
	}
	if result.MetadataEvicted != 2 {
		t.Fatalf("MetadataEvicted = %d, want 2", result.MetadataEvicted)
	}
	if _, err := f.metadata.Get(t.Context(), "release", "newest"); err != nil {
		t.Error("the newest entry was evicted, want it kept")
	}
}

func TestSweep_EnforceRecordsTheMeasurement(t *testing.T) {
	f := newFixture(t)
	f.writeImage(t, "a.webp", 42)

	if _, err := f.sweep.Enforce(t.Context(), generousBudget); err != nil {
		t.Fatalf("Enforce: %v", err)
	}

	measurement, found := f.settings.Snapshot().CacheMeasurement()
	if !found {
		t.Fatal("CacheMeasurement not recorded after Enforce")
	}
	if measurement.ImagesBytes != 42 {
		t.Errorf("ImagesBytes = %d, want 42", measurement.ImagesBytes)
	}
}

func TestSweep_ClearRemovesEverythingAndZeroesTheMeasurement(t *testing.T) {
	f := newFixture(t)
	f.writeImage(t, "a.webp", 10)
	if err := f.metadata.Set(t.Context(), "release", "x", metacache.Entry{Body: "hello", FetchedAt: f.clock.Now()}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	if err := f.sweep.Clear(t.Context()); err != nil {
		t.Fatalf("Clear: %v", err)
	}

	report, err := f.sweep.Measure(t.Context())
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}
	if report.Images.Bytes != 0 || report.Metadata.Bytes != 0 {
		t.Errorf("report after Clear = %+v, want both namespaces empty", report)
	}
}

// A budget so small it forces evicting everything must still terminate
// promptly rather than looping -- proven directly (a real timeout below),
// not just reasoned about from the code.
func TestSweep_EnforceTerminatesWhenTheBudgetForcesFullEviction(t *testing.T) {
	f := newFixture(t)
	if err := f.metadata.Set(t.Context(), "release", "x", metacache.Entry{Body: "x", FetchedAt: f.clock.Now()}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	budget := settings.CacheBudget{MaxBytes: 0, MaxAgeDays: 3650, ImagesSharePercent: 0, MetadataSharePercent: 100}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := f.sweep.Enforce(t.Context(), budget); err != nil {
			t.Errorf("Enforce: %v", err)
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Enforce did not return -- an unmeetable budget looped forever")
	}
}
