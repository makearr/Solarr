package adapters_test

import (
	"context"
	"testing"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/secrets"
)

func TestProwlarrSyncFollowsOptionsAndPauses(t *testing.T) {
	cache := newSweepCache(t)
	cipher, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}
	cache.EnableSecrets(cipher)
	instance, err := cache.CreateArrInstance(t.Context(), settings.ArrInstanceRequest{
		App: settings.ArrAppProwlarr, Name: "Prowlarr", BaseURL: "http://prowlarr:9697", Enabled: true,
	}, secrets.Secret("key"), 1)
	if err != nil {
		t.Fatalf("CreateArrInstance: %v", err)
	}
	listed := []indexer.ProwlarrIndexer{
		{ProwlarrID: 1, Name: "Active", Impl: "newznab", Enabled: true, Supported: true, BaseURL: "http://prowlarr:9697/1", Priority: 10},
		{ProwlarrID: 2, Name: "Paused", Impl: "torznab", Enabled: false, Supported: true, BaseURL: "http://prowlarr:9697/2", Priority: 30},
		{ProwlarrID: 3, Name: "Console only", Impl: "newznab", Enabled: true, Supported: false, BaseURL: "http://prowlarr:9697/3", Priority: 25},
	}
	prowlarr := adapters.Prowlarr{
		Cache: cache, Clock: clock.NewFakeAtEpoch(),
		List: func(context.Context, string, string) ([]indexer.ProwlarrIndexer, error) { return listed, nil },
	}

	result, err := prowlarr.SyncIndexers(t.Context(), instance.ID)
	if err != nil || result.Added != 1 || result.Skipped != 2 {
		t.Fatalf("first sync = %+v, %v; want 1 added (the paused and the unsupported skipped by default)", result, err)
	}

	if err := cache.SetArrInstanceSyncOptions(t.Context(), instance.ID, settings.ProwlarrSync{ActiveIndexers: true, PausedIndexers: true, SupportedOnly: true}, 2); err != nil {
		t.Fatalf("SetArrInstanceSyncOptions: %v", err)
	}
	if result, err = prowlarr.SyncIndexers(t.Context(), instance.ID); err != nil || result.Added != 1 || result.Updated != 1 {
		t.Fatalf("second sync = %+v, %v; want the paused one added, the first updated", result, err)
	}
	indexers, _ := cache.Indexers(t.Context())
	byName := map[string]settings.Indexer{}
	for _, found := range indexers {
		byName[found.Name] = found
	}
	if len(byName) != 2 || !byName["Active"].Enabled || byName["Paused"].Enabled || byName["Paused"].Priority != 30 {
		t.Fatalf("indexers = %+v", byName)
	}

	// A pause made here survives a sync while Prowlarr's own flag has not changed; Prowlarr pausing wins when it does.
	paused := byName["Active"]
	if _, err := cache.UpdateIndexer(t.Context(), paused.ID, settings.IndexerRequest{
		Name: paused.Name, Impl: paused.Impl, BaseURL: paused.BaseURL, Enabled: false, Priority: 7, Source: paused.Source(),
	}, 3); err != nil {
		t.Fatalf("UpdateIndexer: %v", err)
	}
	if _, err = prowlarr.SyncIndexers(t.Context(), instance.ID); err != nil {
		t.Fatalf("third sync: %v", err)
	}
	after, _ := cache.GetIndexer(t.Context(), paused.ID)
	if after.Enabled || after.Priority != 7 {
		t.Errorf("local pause and priority were overwritten: enabled=%v priority=%d", after.Enabled, after.Priority)
	}
	listed[0].Enabled = false
	if result, err = prowlarr.SyncIndexers(t.Context(), instance.ID); err != nil || result.Updated != 2 || result.Paused != 0 {
		t.Fatalf("fourth sync = %+v, %v; want both updated (its pause was already mirrored here)", result, err)
	}
}
