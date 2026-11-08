package adapters_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/importing/arrimport"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
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

func TestProwlarrSyncGivesIndexersTheProxyTheyShareATagWith(t *testing.T) {
	cache := newSweepCache(t)
	cipher, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}
	cache.EnableSecrets(cipher)
	instance, err := cache.CreateArrInstance(t.Context(), settings.ArrInstanceRequest{
		App: settings.ArrAppProwlarr, Name: "P", BaseURL: "http://prowlarr:9697", Enabled: true,
	}, secrets.Secret("key"), 1)
	if err != nil {
		t.Fatalf("CreateArrInstance: %v", err)
	}
	prowlarr := adapters.Prowlarr{
		Cache: cache, Clock: clock.NewFakeAtEpoch(),
		List: func(context.Context, string, string) ([]indexer.ProwlarrIndexer, error) {
			return []indexer.ProwlarrIndexer{
				{ProwlarrID: 1, Name: "Tagged", Impl: "torznab", Enabled: true, Supported: true, BaseURL: "http://prowlarr:9697/1", Tags: []int64{1}},
				{ProwlarrID: 2, Name: "Plain", Impl: "torznab", Enabled: true, Supported: true, BaseURL: "http://prowlarr:9697/2"},
			}, nil
		},
		Proxies: func(context.Context, string, string) ([]indexer.ProwlarrProxy, error) {
			return []indexer.ProwlarrProxy{{ProwlarrID: 9, Name: "FlareSolverr", Kind: "flaresolverr", URL: "http://localhost:8191/", Tags: []int64{1}}}, nil
		},
	}
	for range 2 {
		if _, err := prowlarr.SyncIndexers(t.Context(), instance.ID); err != nil {
			t.Fatalf("SyncIndexers: %v", err)
		}
	}
	proxies, _ := cache.Proxies(t.Context())
	indexers, _ := cache.Indexers(t.Context())
	if len(proxies) == 1 && proxies[0].URL != "http://prowlarr:8191/" {
		t.Errorf("proxy address = %q, want Prowlarr's localhost as Prowlarr's host", proxies[0].URL)
	}
	if len(proxies) != 1 || proxies[0].Kind != settings.ProxyFlareSolverr {
		t.Fatalf("proxies = %+v, want the one FlareSolverr, not duplicated by a second sync", proxies)
	}
	for _, found := range indexers {
		used := found.ProxyID()
		if (found.Name == "Tagged") != (used != nil && *used == proxies[0].ID) {
			t.Errorf("%s proxy = %v", found.Name, used)
		}
	}
}

// A client that signs in without a user name or secret is created; one that needs an API key is only counted.
func TestProwlarrSyncAddsDownloadClientsThatNeedNoSecret(t *testing.T) {
	cache := newSweepCache(t)
	cipher, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}
	cache.EnableSecrets(cipher)
	served := `[
	 {"name":"qBit","implementation":"QBittorrent","protocol":"torrent","enable":true,"priority":3,"fields":[{"name":"host","value":"10.0.1.134"},{"name":"port","value":8086},{"name":"category","value":"prowlarr"}]},
	 {"name":"SAB","implementation":"Sabnzbd","protocol":"usenet","enable":true,"priority":1,"fields":[{"name":"host","value":"10.0.1.134"},{"name":"port","value":8080}]}]`
	instance, err := cache.CreateArrInstance(t.Context(), settings.ArrInstanceRequest{
		App: settings.ArrAppProwlarr, Name: "P", BaseURL: "http://prowlarr.local", Enabled: true,
	}, secrets.Secret("key"), 1)
	if err != nil {
		t.Fatalf("CreateArrInstance: %v", err)
	}
	prowlarr := adapters.Prowlarr{
		Cache: cache, Clock: clock.NewFakeAtEpoch(),
		List: func(context.Context, string, string) ([]indexer.ProwlarrIndexer, error) { return nil, nil },
		Lidarr: func(baseURL, apiKey string) arrimport.LidarrClient {
			fake := clock.NewFakeAtEpoch()
			limiter := httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{RequestsPerMinute: 6000}}, httpx.NoJitter)
			breaker := httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute})
			return arrimport.NewLidarrClient(httpx.NewClient(fake, httpx.NewGuard(nil, nil, nil), limiter, breaker, httpx.ClientConfig{
				Provider: "prowlarr-test", UserAgent: "test", Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(served))}, nil
				}),
			}), baseURL, apiKey)
		},
	}
	result, err := prowlarr.SyncIndexers(t.Context(), instance.ID)
	if err != nil || result.ClientsAdded != 1 || result.ClientsWaiting != 1 {
		t.Fatalf("sync = %+v, %v; want qBittorrent added and SABnzbd waiting for its key", result, err)
	}
	clients, _ := cache.Downloaders(t.Context())
	if len(clients) != 1 || clients[0].Impl != "qbittorrent" || clients[0].Options().PriorityRank() != 3 || clients[0].Options().Category != "prowlarr" {
		t.Errorf("clients = %+v", clients)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestProwlarrSyncTellsTwoConnectionsSameNamedIndexersApart(t *testing.T) {
	cache := newSweepCache(t)
	cipher, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}
	cache.EnableSecrets(cipher)
	listed := func(base string) func(context.Context, string, string) ([]indexer.ProwlarrIndexer, error) {
		return func(context.Context, string, string) ([]indexer.ProwlarrIndexer, error) {
			return []indexer.ProwlarrIndexer{{ProwlarrID: 1, Name: "NZBgeek", Impl: "newznab", Enabled: true, Supported: true, BaseURL: base + "/1"}}, nil
		}
	}
	for index, name := range []string{"One", "Two"} {
		base := "http://prowlarr" + name
		instance, err := cache.CreateArrInstance(t.Context(), settings.ArrInstanceRequest{App: settings.ArrAppProwlarr, Name: name, BaseURL: base, Enabled: true}, secrets.Secret("k"), 1)
		if err != nil {
			t.Fatalf("CreateArrInstance: %v", err)
		}
		prowlarr := adapters.Prowlarr{Cache: cache, Clock: clock.NewFakeAtEpoch(), List: listed(base)}
		if result, err := prowlarr.SyncIndexers(t.Context(), instance.ID); err != nil || result.Added != 1 {
			t.Fatalf("sync %d = %+v, %v", index, result, err)
		}
	}
	indexers, _ := cache.Indexers(t.Context())
	if len(indexers) != 2 || indexers[0].Name == indexers[1].Name {
		t.Errorf("indexers = %+v, want two with different names", indexers)
	}
}
