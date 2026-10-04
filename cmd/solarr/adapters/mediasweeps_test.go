package adapters_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/scanning/scan"
	"github.com/makearr/Solarr/migrations"
)

func newSweepCache(t *testing.T) *settings.Cache {
	t.Helper()
	directory := t.TempDir()
	backups := filepath.Join(directory, "backups")
	if err := os.MkdirAll(backups, 0o750); err != nil {
		t.Fatalf("create backup directory: %v", err)
	}
	opened, err := store.Open(t.Context(), clock.NewFakeAtEpoch(), store.Config{
		Name: "settings", Path: filepath.Join(directory, "settings.db"), Logger: slog.New(slog.DiscardHandler),
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

// namedConsumer tells two sweeps apart.
type namedConsumer string

func (namedConsumer) Observe(context.Context, settings.RootFolder, int64, []scan.Observed) error {
	return nil
}

// sweeping is a media type that scans with the consumer it names; an empty name declares no consumer.
type sweeping struct {
	key      media.Key
	consumer namedConsumer
}

func (s sweeping) Descriptor() media.Descriptor { return media.Descriptor{Key: s.key} }

func (s sweeping) ScanSweep() scan.Sweep {
	if s.consumer == "" {
		return scan.Sweep{}
	}
	return scan.Sweep{Consumer: s.consumer}
}

// declared is a media type with a descriptor and nothing else.
type declared struct{ key media.Key }

func (d declared) Descriptor() media.Descriptor { return media.Descriptor{Key: d.key} }

func TestMediaSweeps_AnswersTheSweepOfTheLibrarysOwnMediaType(t *testing.T) {
	cache := newSweepCache(t)
	registry, err := media.NewRegistry(
		sweeping{key: "music", consumer: "music's"}, sweeping{key: "tv", consumer: "tv's"},
		sweeping{key: "movie"}, declared{key: "book"},
	)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	ids := map[string]int64{}
	for _, mediaType := range []string{"music", "tv", "movie", "book", "comic"} {
		category, err := cache.CreateCategory(t.Context(), mediaType, mediaType, 10)
		if err != nil {
			t.Fatalf("CreateCategory %s: %v", mediaType, err)
		}
		ids[mediaType] = category.ID
	}
	sweeps := adapters.MediaSweeps{Registry: registry, Cache: cache}

	type answer struct {
		consumer scan.Consumer
		found    bool
	}
	want := map[string]answer{
		"music": {namedConsumer("music's"), true},
		"tv":    {namedConsumer("tv's"), true},
		// Declares the capability but no consumer, has no scanning at all, has no module.
		"movie": {nil, false}, "book": {nil, false}, "comic": {nil, false},
	}
	for mediaType, expected := range want {
		sweep, found := sweeps.SweepFor(ids[mediaType])
		if got := (answer{sweep.Consumer, found}); got != expected {
			t.Errorf("SweepFor(%s library) = %+v, want %+v", mediaType, got, expected)
		}
	}
	if _, found := sweeps.SweepFor(9999); found {
		t.Error("SweepFor answered a sweep for a category that does not exist")
	}
}
