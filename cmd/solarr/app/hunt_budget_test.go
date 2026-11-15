package app

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
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
