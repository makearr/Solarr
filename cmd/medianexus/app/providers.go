package app

import (
	"net/url"
	"time"

	"github.com/makearr/MediaNexus/cmd/medianexus/adapters"
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

// musicBrainzProvider builds ADR-29's provider: one shared rate-limit and circuit
// breaker registry (ADR-13) across both sources, so a proxy failure counts against
// only the proxy's own budget and never steals from direct MusicBrainz's.
func (p *process) musicBrainzProvider() musicbrainz.Provider {
	wall := clock.NewSystem()
	guard := httpx.NewGuard(nil, nil, nil)
	limiter := httpx.NewLimiter(wall, httpx.Config{
		Providers: map[string]httpx.Limits{
			// MusicBrainz allows one request per second and means it (ADR-29).
			musicbrainz.DirectName: {MinInterval: time.Second},
			// The proxy is Lidarr's own service, not rate-limited by ADR-29's
			// 1 req/s rule; kept modest anyway since it is shared infrastructure
			// this project does not operate.
			musicbrainz.ProxyName: {MinInterval: 250 * time.Millisecond},
		},
	}, nil)
	breaker := httpx.NewBreaker(wall, httpx.BreakerConfig{})

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
// than one per instance.
func (p *process) arrClientFactory() adapters.LidarrClientFactory {
	wall := clock.NewSystem()
	guard := httpx.NewGuard(nil, nil, nil)
	limiter := httpx.NewLimiter(wall, httpx.Config{
		Providers: map[string]httpx.Limits{"arr": {MinInterval: 100 * time.Millisecond}},
	}, nil)
	breaker := httpx.NewBreaker(wall, httpx.BreakerConfig{})
	agent := httpx.UserAgent(p.build.Version)

	return func(baseURL, apiKey string) arrimport.LidarrClient {
		client := httpx.NewClient(wall, guard, limiter, breaker, httpx.ClientConfig{
			Provider: "arr", UserAgent: agent, Proxy: p.liveProxyPolicy(),
		})
		return arrimport.NewLidarrClient(client, baseURL, apiKey)
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
