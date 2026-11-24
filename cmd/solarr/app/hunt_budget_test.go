package app

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/secrets"
)

// The hunt's budget follows the enabled indexers, whatever categories they list: a TV-only tracker is an indexer (HUNT-4).
func TestSearchesPerHour_CountsEnabledIndexersNotMusicConnections(t *testing.T) {
	server := newGoldenServer(t)
	running := server.process
	perHour := int(*running.cache.Snapshot().PrioritySettings().SearchesPerHour)
	if got, err := running.searchesPerHour(t.Context()); err != nil || got != 0 {
		t.Fatalf("budget with no indexer = %d, %v; want 0", got, err)
	}
	if _, err := running.cache.CreateIndexer(t.Context(), settings.IndexerRequest{
		Name: "tv-only", Impl: "newznab", BaseURL: "https://indexer.example.test", Enabled: true, Priority: 25,
	}, secrets.Secret("key"), 1); err != nil {
		t.Fatal(err)
	}
	if got, err := running.searchesPerHour(t.Context()); err != nil || got != perHour {
		t.Fatalf("budget with one enabled indexer = %d, %v; want %d", got, err, perHour)
	}
}

func TestSearchesPerHour_SafeModeSpendsNothingWithAnIndexerEnabled(t *testing.T) {
	running := newGoldenServer(t).process
	if _, err := running.cache.CreateIndexer(t.Context(), settings.IndexerRequest{
		Name: "tv-only", Impl: "newznab", BaseURL: "https://indexer.example.test", Enabled: true, Priority: 25,
	}, secrets.Secret("key"), 1); err != nil {
		t.Fatal(err)
	}
	running.config.SafeMode = true
	if got, err := running.searchesPerHour(t.Context()); err != nil || got != 0 {
		t.Fatalf("budget in safe mode = %d, %v; want 0", got, err)
	}
}

func TestUnlessSafe_RunsTheJobOnlyOutsideSafeMode(t *testing.T) {
	running := newGoldenServer(t).process
	runs := 0
	handler := running.unlessSafe(jobs.HandlerFunc(func(context.Context, jobs.Job) error { runs++; return nil }))
	if err := handler.Run(t.Context(), jobs.Job{}); err != nil || runs != 1 {
		t.Fatalf("runs = %d, %v; want 1", runs, err)
	}
	running.config.SafeMode = true
	if err := handler.Run(t.Context(), jobs.Job{}); err != nil || runs != 1 {
		t.Fatalf("runs in safe mode = %d, %v; want still 1", runs, err)
	}
}

func TestUnlessSafe_SaysOnceWhyItSkippedARun(t *testing.T) {
	running := newGoldenServer(t).process
	var out bytes.Buffer
	running.logger = slog.New(slog.NewTextHandler(&out, nil))
	running.config.SafeMode = true
	handler := running.unlessSafe(jobs.HandlerFunc(func(context.Context, jobs.Job) error { return nil }))
	if err := handler.Run(t.Context(), jobs.Job{Type: "rss.sync"}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); strings.Count(got, "skipped: safe mode") != 1 || !strings.Contains(got, "job=rss.sync") {
		t.Errorf("log = %q, want one line naming the job", got)
	}
}
