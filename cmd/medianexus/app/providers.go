package app

import (
	"net/url"
	"strconv"
	"time"

	"github.com/makearr/MediaNexus/cmd/medianexus/adapters"
	"github.com/makearr/MediaNexus/internal/acquiring/indexer"
	"github.com/makearr/MediaNexus/internal/acquiring/sabnzbd"
	"github.com/makearr/MediaNexus/internal/db/settings"
	"github.com/makearr/MediaNexus/internal/importing/arrimport"
	"github.com/makearr/MediaNexus/internal/media/music"
	"github.com/makearr/MediaNexus/internal/metadata/metacache"
	"github.com/makearr/MediaNexus/internal/metadata/musicbrainz"
	"github.com/makearr/MediaNexus/internal/metadata/wikidata"
	"github.com/makearr/MediaNexus/internal/platform/clock"
	"github.com/makearr/MediaNexus/internal/platform/httpx"
)

// liveProxyPolicy is ADR-58's egress proxy, read live so every client shares
// one closure and a Network screen change applies with no restart.
func (p *process) liveProxyPolicy() httpx.LiveProxyPolicy {
	return func() httpx.ProxyPolicy {
		network := p.cache.Snapshot().NetworkSettings()
		if network.ProxyURL == "" {
			return httpx.ProxyPolicy{}
		}
		parsed, err := url.Parse(network.ProxyURL)
		if err != nil {
			p.logger.Warn("configured egress proxy no longer parses", "error", err)
			return httpx.ProxyPolicy{}
		}
		return httpx.ProxyPolicy{URL: parsed, Bypass: network.ProxyBypass}
	}
}

// wikidataFetcher builds ADR-8's biography lookup: two hosts, two ADR-13
// rate-limit/breaker buckets, neither shared with MusicBrainz's own (a
// Wikidata or Wikipedia outage must never count against the MusicBrainz
// breaker, and vice versa).
func (p *process) wikidataFetcher() music.Biographer {
	wall := clock.NewSystem()
	guard := httpx.NewGuard(nil, nil, nil)
	agent := httpx.UserAgent(p.build.Version)

	newHostClient := func(providerName string) *httpx.Client {
		limiter := httpx.NewLimiter(wall, httpx.Config{
			Providers: map[string]httpx.Limits{providerName: {MinInterval: time.Second}},
		}, nil)
		breaker := httpx.NewBreaker(wall, httpx.BreakerConfig{})
		return httpx.NewClient(wall, guard, limiter, breaker, httpx.ClientConfig{
			Provider: providerName, UserAgent: agent, Proxy: p.liveProxyPolicy(),
		})
	}

	return wikidata.New(wikidata.Config{
		WikidataClient:  newHostClient("wikidata"),
		WikipediaClient: newHostClient("wikipedia"),
	})
}

// musicBrainzBudget is the one rate limiter and circuit breaker every
// MusicBrainz provider in the process draws on (ADR-13).
type musicBrainzBudget struct {
	limiter *httpx.Limiter
	breaker *httpx.Breaker
}

// musicBrainzBudget answers the process's single budget. Each caller of
// musicBrainzProvider used to build its own, so scan, enrichment, import and
// subscriptions each took a full 1 req/s; running together they exceeded it
// and MusicBrainz answered 503 (found 2026-09-17).
func (p *process) musicBrainzBudget() musicBrainzBudget {
	p.musicBrainzOnce.Do(func() {
		wall := clock.NewSystem()
		p.musicBrainzShared = musicBrainzBudget{
			limiter: httpx.NewLimiter(wall, httpx.Config{
				Providers: map[string]httpx.Limits{
					// MusicBrainz allows one request per second and means it (ADR-29).
					musicbrainz.DirectName: {MinInterval: time.Second},
					// The proxy is Lidarr's own service, not rate-limited by ADR-29's
					// 1 req/s rule; kept modest anyway since it is shared infrastructure
					// this project does not operate.
					musicbrainz.ProxyName: {MinInterval: 250 * time.Millisecond},
				},
			}, nil),
			breaker: httpx.NewBreaker(wall, httpx.BreakerConfig{}),
		}
	})
	return p.musicBrainzShared
}

// musicBrainzProvider builds ADR-29's provider on the process's one shared
// rate-limit and circuit breaker registry (ADR-13), across both sources, so a
// proxy failure counts against only the proxy's own budget and never steals
// from direct MusicBrainz's.
func (p *process) musicBrainzProvider() musicbrainz.Provider {
	wall := clock.NewSystem()
	guard := httpx.NewGuard(nil, nil, nil)
	budget := p.musicBrainzBudget()
	limiter, breaker := budget.limiter, budget.breaker

	agent := httpx.UserAgent(p.build.Version)
	config := p.config.Metadata.MusicBrainz
	direct := musicbrainz.NewDirect(httpx.NewClient(wall, guard, limiter, breaker, httpx.ClientConfig{
		Provider: musicbrainz.DirectName, UserAgent: agent, Proxy: p.liveProxyPolicy(),
	}), config.BaseURL)

	sources := []musicbrainz.Source{direct}
	if config.ProxyEnabled {
		proxy := musicbrainz.NewProxy(httpx.NewClient(wall, guard, limiter, breaker, httpx.ClientConfig{
			Provider: musicbrainz.ProxyName, UserAgent: agent, Proxy: p.liveProxyPolicy(),
		}), config.ProxyBaseURL)
		// The proxy goes first (ADR-29: "preferred, never required") -- direct
		// MusicBrainz is what a proxy failure (the breaker opening) falls back
		// to, so it stays in the list either way.
		sources = []musicbrainz.Source{proxy, direct}
	}

	return musicbrainz.New(musicbrainz.Config{
		Sources: sources, Cache: metacache.New(p.metadata), Clock: wall, Logger: p.logger,
		RefreshAfter: config.RefreshAfter.AsDuration(),
	})
}

// arrClientFactory builds adapters.LidarrClientFactory: one shared rate-
// limit/breaker bucket across every *Arr instance sync (todo 4.13) -- these
// are rare, admin-triggered, one-at-a-time operations, not a high-frequency
// provider like MusicBrainz, so one shared "arr" bucket is enough rather
// than one per instance. Deliberately gentle (2 req/s, a 60s per-request
// timeout well above httpx's own 30s default): the *Arr instance on the
// other end is someone's own real server, often also busy with its own
// imports, not infrastructure this project operates.
//
// The guard is built fresh per call, allowing baseURL's own host -- an
// *Arr instance lives on the LAN or a Docker network in almost every real
// deployment (ADR-52 is explicitly about a self-hosted instance), and an
// admin-entered, persisted connection is exactly the "opt-out for indexers
// that genuinely live on the LAN" httpx.Guard's own allowedHosts already
// exists for (its own doc comment), not the attacker-controlled, per-request
// URL the guard exists to stop. Found needed live: a real sync against the
// user's own Lidarr on a private address was refused outright until this.
func (p *process) arrClientFactory() adapters.LidarrClientFactory {
	wall := clock.NewSystem()
	limiter := httpx.NewLimiter(wall, httpx.Config{
		Providers: map[string]httpx.Limits{"arr": {MinInterval: 500 * time.Millisecond}},
	}, nil)
	breaker := httpx.NewBreaker(wall, httpx.BreakerConfig{})
	agent := httpx.UserAgent(p.build.Version)

	return func(baseURL, apiKey string) arrimport.LidarrClient {
		var allowedHosts []string
		if parsed, err := url.Parse(baseURL); err == nil && parsed.Hostname() != "" {
			allowedHosts = []string{parsed.Hostname()}
		}
		guard := httpx.NewGuard(nil, nil, allowedHosts)
		client := httpx.NewClient(wall, guard, limiter, breaker, httpx.ClientConfig{
			Provider: "arr", UserAgent: agent, Proxy: p.liveProxyPolicy(), Timeout: 60 * time.Second,
		})
		return arrimport.NewLidarrClient(client, baseURL, apiKey)
	}
}

// indexerLiveProxyPolicy resolves ADR-47's own per-indexer egress proxy when
// the connection has one configured, falling back to ADR-58's global egress
// proxy otherwise -- an indexer with no egress block behaves exactly as
// before. The SSRF guard is unaffected either way: Proxy only ever selects
// which URL a request is tunnelled through, never which host the guard
// resolves and checks (httpx.ProxyDial's own job, unchanged here).
func (p *process) indexerLiveProxyPolicy(egress settings.IndexerEgress) httpx.LiveProxyPolicy {
	if egress.ProxyURL == "" {
		return p.liveProxyPolicy()
	}
	parsed, err := url.Parse(egress.ProxyURL)
	if err != nil {
		p.logger.Warn("configured indexer egress proxy does not parse", "error", err)
		return p.liveProxyPolicy()
	}
	return func() httpx.ProxyPolicy { return httpx.ProxyPolicy{URL: parsed} }
}

// indexerClientFactory builds adapters.IndexerClientFactory. Each indexer
// connection gets its own rate-limit/breaker bucket, keyed by id -- ADR-37's
// own "each indexer draws on its own bucket, so parallelism never turns into
// a burst against one provider", needed the moment a search fans out to more
// than one indexer concurrently (stage 5's own candidate pipeline). A bucket
// is created lazily by the limiter/breaker registries the first time an id
// is used, so a newly added indexer needs no code change here.
func (p *process) indexerClientFactory() adapters.IndexerClientFactory {
	wall := clock.NewSystem()
	limiter := httpx.NewLimiter(wall, httpx.Config{
		Defaults: httpx.Limits{MinInterval: 500 * time.Millisecond},
	}, nil)
	breaker := httpx.NewBreaker(wall, httpx.BreakerConfig{})
	agent := httpx.UserAgent(p.build.Version)
	guard := httpx.NewGuard(nil, nil, nil)

	return func(indexerID int64, baseURL, apiKey string, egress settings.IndexerEgress) indexer.Client {
		client := httpx.NewClient(wall, guard, limiter, breaker, httpx.ClientConfig{
			Provider: "indexer:" + strconv.FormatInt(indexerID, 10), UserAgent: agent,
			Proxy: p.indexerLiveProxyPolicy(egress),
		})
		return indexer.NewClient(client, baseURL, apiKey)
	}
}

// downloaderClientFactory builds adapters.DownloaderClientFactory, one
// rate-limit/breaker bucket per connection id -- gentle by default (a
// downloader is polled, not fanned out to), the global egress proxy applies
// like any other outbound client (ADR-47 is indexer-specific, not this).
func (p *process) downloaderClientFactory() adapters.DownloaderClientFactory {
	wall := clock.NewSystem()
	limiter := httpx.NewLimiter(wall, httpx.Config{
		Defaults: httpx.Limits{MinInterval: 200 * time.Millisecond},
	}, nil)
	breaker := httpx.NewBreaker(wall, httpx.BreakerConfig{})
	agent := httpx.UserAgent(p.build.Version)
	guard := httpx.NewGuard(nil, nil, nil)

	return func(downloaderID int64, baseURL, credentials string) sabnzbd.Client {
		client := httpx.NewClient(wall, guard, limiter, breaker, httpx.ClientConfig{
			Provider: "downloader:" + strconv.FormatInt(downloaderID, 10), UserAgent: agent,
			Proxy: p.liveProxyPolicy(),
		})
		return sabnzbd.NewClient(client, baseURL, credentials)
	}
}

// publicIPClient is the one outbound client the process builds for itself. Every
// other client belongs to a provider and is built where that provider is
// configured (ADR-13).
func (p *process) publicIPClient() *httpx.Client {
	wall := clock.NewSystem()
	// Nil means the system resolver and dialer; the SSRF check still applies.
	guard := httpx.NewGuard(nil, nil, nil)
	return httpx.NewClient(wall, guard, httpx.NewLimiter(wall, httpx.Config{}, nil),
		httpx.NewBreaker(wall, httpx.BreakerConfig{}), httpx.ClientConfig{
			Provider:  "public-ip",
			UserAgent: httpx.UserAgent(p.build.Version),
			Timeout:   10 * time.Second,
			Proxy:     p.liveProxyPolicy(),
		})
}
