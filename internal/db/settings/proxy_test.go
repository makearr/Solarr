package settings_test

import (
	"errors"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/secrets"
)

func TestProxyListResolvesIntoIndexerEgressAndRefusesDeleteInUse(t *testing.T) {
	cache, _ := newCache(t)
	cipher, err := secrets.New(testCipherKey())
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}
	cache.EnableSecrets(cipher)
	ctx := t.Context()

	if _, err := cache.CreateProxy(ctx, settings.ProxyRequest{Name: "bad", Kind: settings.ProxySOCKS5, URL: "http://x:1"}, 1); err == nil {
		t.Fatal("a socks5 proxy with an http address was accepted")
	}
	solver, err := cache.CreateProxy(ctx, settings.ProxyRequest{Name: "Flare", Kind: settings.ProxyFlareSolverr, URL: "http://10.0.0.5:8191"}, 1)
	if err != nil {
		t.Fatalf("CreateProxy: %v", err)
	}
	plain, err := cache.CreateProxy(ctx, settings.ProxyRequest{Name: "Squid", Kind: settings.ProxyHTTP, URL: "http://10.0.0.6:3128"}, 1)
	if err != nil {
		t.Fatalf("CreateProxy: %v", err)
	}
	indexer, err := cache.CreateIndexer(ctx, settings.IndexerRequest{
		Name: "idx", Impl: "newznab", BaseURL: "http://i", Enabled: true, ProxyID: &solver.ID,
	}, secrets.Secret(""), 1)
	if err != nil {
		t.Fatalf("CreateIndexer: %v", err)
	}

	if got := cache.ResolveEgress(ctx, indexer); got.FlareSolverrURL != solver.URL || got.ProxyURL != "" {
		t.Errorf("egress = %+v, want FlareSolverr %s", got, solver.URL)
	}
	indexer, _ = cache.UpdateIndexer(ctx, indexer.ID, settings.IndexerRequest{
		Name: "idx", Impl: "newznab", BaseURL: "http://i", Enabled: true, ProxyID: &plain.ID,
	}, 2)
	if got := cache.ResolveEgress(ctx, indexer); got.ProxyURL != plain.URL || got.FlareSolverrURL != "" {
		t.Errorf("egress = %+v, want proxy %s", got, plain.URL)
	}
	if err := cache.DeleteProxy(ctx, plain.ID); !errors.Is(err, settings.ErrProxyInUse) {
		t.Errorf("deleting a proxy in use: %v, want ErrProxyInUse", err)
	}
	if err := cache.DeleteProxy(ctx, solver.ID); err != nil {
		t.Errorf("deleting an unused proxy: %v", err)
	}
}
