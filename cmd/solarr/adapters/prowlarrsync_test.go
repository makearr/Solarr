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

func TestProwlarrSyncTakesOverTheSitesBehindProwlarr(t *testing.T) {
	cache := newSweepCache(t)
	cipher, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}
	cache.EnableSecrets(cipher)
	instance, err := cache.CreateArrInstance(t.Context(), settings.ArrInstanceRequest{
		App: settings.ArrAppProwlarr, Name: "Prowlarr", BaseURL: "http://prowlarr:9697", Enabled: true,
	}, secrets.Secret("prowlarr-key"), 1)
	if err != nil {
		t.Fatalf("CreateArrInstance: %v", err)
	}
	listed := []indexer.ProwlarrIndexer{
		{ProwlarrID: 1, Name: "Hidden", Impl: "newznab", Enabled: true, Supported: true, BaseURL: "http://prowlarr:9697/1",
			Upstream: &indexer.UpstreamIndexer{Impl: "newznab", BaseURL: "https://api.hidden.example", KeyHidden: true}},
		{ProwlarrID: 2, Name: "Open", Impl: "newznab", Enabled: true, Supported: true, BaseURL: "http://prowlarr:9697/2",
			Upstream: &indexer.UpstreamIndexer{Impl: "newznab", BaseURL: "https://open.example", Key: "site-key"}},
		{ProwlarrID: 3, Name: "Only Prowlarr", Impl: "torznab", Enabled: true, Supported: true, BaseURL: "http://prowlarr:9697/3"},
	}
	prowlarr := adapters.Prowlarr{
		Cache: cache, Clock: clock.NewFakeAtEpoch(),
		List: func(context.Context, string, string) ([]indexer.ProwlarrIndexer, error) { return listed, nil },
	}
	byName := func() map[string]settings.Indexer {
		indexers, _ := cache.Indexers(t.Context())
		named := map[string]settings.Indexer{}
		for _, found := range indexers {
			named[found.Name] = found
		}
		return named
	}
	keyOf := func(found settings.Indexer) string {
		key, err := cache.DecryptedIndexerAPIKey(t.Context(), found.ID)
		if err != nil {
			t.Fatalf("DecryptedIndexerAPIKey: %v", err)
		}
		return key.Reveal()
	}

	if _, err := prowlarr.SyncIndexers(t.Context(), instance.ID); err != nil {
		t.Fatalf("SyncIndexers: %v", err)
	}
	found := byName()
	if got := found["Open"]; got.BaseURL != "https://open.example" || keyOf(got) != "site-key" {
		t.Errorf("Open = %s with key %q; want the site itself with Prowlarr's key for it", got.BaseURL, keyOf(got))
	}
	if got := found["Hidden"]; got.BaseURL != "http://prowlarr:9697/1" || keyOf(got) != "prowlarr-key" || got.Source().Upstream.BaseURL != "https://api.hidden.example" {
		t.Errorf("Hidden = %s, upstream %+v; want Prowlarr's address until the key is given", got.BaseURL, got.Source().Upstream)
	}
	if got := found["Only Prowlarr"]; got.BaseURL != "http://prowlarr:9697/3" {
		t.Errorf("Only Prowlarr = %s; want Prowlarr's address", got.BaseURL)
	}

	// A second sync leaves the site address and key of a taken-over indexer alone.
	if _, err := prowlarr.SyncIndexers(t.Context(), instance.ID); err != nil {
		t.Fatalf("second SyncIndexers: %v", err)
	}
	if got := byName()["Open"]; got.BaseURL != "https://open.example" || keyOf(got) != "site-key" {
		t.Errorf("after the second sync Open = %s with key %q", got.BaseURL, keyOf(got))
	}

	// Prowlarr as the indexer puts every indexer back on Prowlarr's address and key.
	if err := cache.SetArrInstanceSyncOptions(t.Context(), instance.ID, settings.ProwlarrSync{ActiveIndexers: true, SupportedOnly: true, ProwlarrAsIndexer: true}, 2); err != nil {
		t.Fatalf("SetArrInstanceSyncOptions: %v", err)
	}
	if _, err := prowlarr.SyncIndexers(t.Context(), instance.ID); err != nil {
		t.Fatalf("third SyncIndexers: %v", err)
	}
	if got := byName()["Open"]; got.BaseURL != "http://prowlarr:9697/2" || keyOf(got) != "prowlarr-key" {
		t.Errorf("with Prowlarr as the indexer Open = %s with key %q", got.BaseURL, keyOf(got))
	}
}

func TestProwlarrSyncAddsTheFlareSolverrOnce(t *testing.T) {
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
	if err := cache.SetArrInstanceSyncOptions(t.Context(), instance.ID, settings.ProwlarrSync{FlareSolverrURL: "http://flaresolverr:8191"}, 2); err != nil {
		t.Fatalf("SetArrInstanceSyncOptions: %v", err)
	}
	prowlarr := adapters.Prowlarr{
		Cache: cache, Clock: clock.NewFakeAtEpoch(),
		List: func(context.Context, string, string) ([]indexer.ProwlarrIndexer, error) { return nil, nil },
	}
	for range 2 {
		if _, err := prowlarr.SyncIndexers(t.Context(), instance.ID); err != nil {
			t.Fatalf("SyncIndexers: %v", err)
		}
	}
	proxies, _ := cache.Proxies(t.Context())
	if len(proxies) != 1 || proxies[0].Kind != settings.ProxyFlareSolverr || proxies[0].URL != "http://flaresolverr:8191" {
		t.Errorf("proxies = %+v; want one FlareSolverr", proxies)
	}
}

func TestProwlarrSyncSearchesOneSiteOnce(t *testing.T) {
	cache := newSweepCache(t)
	cipher, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}
	cache.EnableSecrets(cipher)
	var ids []int64
	for _, name := range []string{"First", "Second"} {
		instance, err := cache.CreateArrInstance(t.Context(), settings.ArrInstanceRequest{
			App: settings.ArrAppProwlarr, Name: name, BaseURL: "http://" + name + ":9697", Enabled: true,
		}, secrets.Secret("key"), 1)
		if err != nil {
			t.Fatalf("CreateArrInstance: %v", err)
		}
		ids = append(ids, instance.ID)
	}
	prowlarr := adapters.Prowlarr{
		Cache: cache, Clock: clock.NewFakeAtEpoch(),
		List: func(_ context.Context, baseURL, _ string) ([]indexer.ProwlarrIndexer, error) {
			return []indexer.ProwlarrIndexer{{ProwlarrID: 1, Name: "NZBgeek", Impl: "newznab", Enabled: true, Supported: true, BaseURL: baseURL + "/1",
				Upstream: &indexer.UpstreamIndexer{Impl: "newznab", BaseURL: "https://api.nzbgeek.info/", Key: "k"}}}, nil
		},
		Proxies: func(_ context.Context, _, _ string) ([]indexer.ProwlarrProxy, error) {
			return []indexer.ProwlarrProxy{{ProwlarrID: 1, Name: "FlareSolverr", Kind: "flaresolverr", URL: "http://flaresolverr:8191/"}}, nil
		},
	}
	for _, id := range ids {
		if _, err := prowlarr.SyncIndexers(t.Context(), id); err != nil {
			t.Fatalf("SyncIndexers: %v", err)
		}
	}
	if indexers, _ := cache.Indexers(t.Context()); len(indexers) != 1 {
		t.Errorf("indexers = %d, want NZBgeek once", len(indexers))
	}
	if proxies, _ := cache.Proxies(t.Context()); len(proxies) != 1 {
		t.Errorf("proxies = %d, want FlareSolverr once", len(proxies))
	}
}
