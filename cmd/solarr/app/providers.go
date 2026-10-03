package app

import (
	"context"
	"errors"
	"net/url"
	"path/filepath"
	"strconv"
	"time"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/importing/arrimport"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/metadata/acoustid"
	"github.com/makearr/Solarr/internal/metadata/anilist"
	"github.com/makearr/Solarr/internal/metadata/metacache"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
	"github.com/makearr/Solarr/internal/metadata/skyhook"
	"github.com/makearr/Solarr/internal/metadata/theaudiodb"
	"github.com/makearr/Solarr/internal/metadata/tmdb"
	"github.com/makearr/Solarr/internal/metadata/tvdb"
	"github.com/makearr/Solarr/internal/metadata/tvmaze"
	"github.com/makearr/Solarr/internal/metadata/wikidata"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
	"github.com/makearr/Solarr/internal/platform/httpx"
	"github.com/makearr/Solarr/internal/platform/remoteimage"
)

// liveProxyPolicy is ADR-58's egress proxy, read live so every client shares
// one closure and a Network screen change applies with no restart.
func (p *process) liveProxyPolicy() httpx.LiveProxyPolicy {
	return func() httpx.ProxyPolicy {
		network := p.cache.Snapshot().NetworkSettings()
		if !network.ProxyEnabled || network.ProxyURL == "" {
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

	newHostClient := func(providerName string, minInterval time.Duration) *httpx.Client {
		limiter := httpx.NewLimiter(wall, httpx.Config{
			Providers: map[string]httpx.Limits{providerName: {MinInterval: minInterval}},
		}, nil)
		breaker := httpx.NewBreaker(wall, httpx.BreakerConfig{})
		return httpx.NewClient(wall, guard, limiter, breaker, httpx.ClientConfig{
			Provider: providerName, UserAgent: agent, Proxy: p.liveProxyPolicy(),
		})
	}

	return wikidata.New(wikidata.Config{
		// Wikidata's action API asks for serial requests, so it takes batches; Wikipedia's REST API allows 200/s.
		WikidataClient:  newHostClient("wikidata", time.Second),
		WikipediaClient: newHostClient("wikipedia", 100*time.Millisecond),
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
					// DroppedNeedle's documented 10 req/s for the pool; BrainzMash publishes no quota.
					musicbrainz.BrainzMashName: {MinInterval: 100 * time.Millisecond},
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

// acoustIDLookup answers the process's one AcoustID client: ADR-29's fingerprint
// rung, on its own ADR-13 bucket at the 3 req/s acoustid.org publishes.
func (p *process) acoustIDLookup() acoustid.Client {
	p.acoustIDOnce.Do(func() {
		wall := clock.NewSystem()
		limiter := httpx.NewLimiter(wall, httpx.Config{
			Providers: map[string]httpx.Limits{acoustid.Name: {MinInterval: acoustIDMinInterval}},
		}, nil)
		client := httpx.NewClient(wall, httpx.NewGuard(nil, nil, nil), limiter,
			httpx.NewBreaker(wall, httpx.BreakerConfig{}), httpx.ClientConfig{
				Provider: acoustid.Name, UserAgent: httpx.UserAgent(p.build.Version), Proxy: p.liveProxyPolicy(),
			})
		p.acoustIDShared = acoustid.New(client, "", p.acoustIDKey)
	})
	return p.acoustIDShared
}

// acoustIDMinInterval is acoustid.org's published 3 requests per second.
const acoustIDMinInterval = 334 * time.Millisecond

// acoustIDKey opens the stored key per lookup, so entering one in the interface
// takes effect without a restart; unreadable means the rung is off, not broken.
func (p *process) acoustIDKey() string {
	// Switched off, the rung reads as keyless and is skipped, and re-run when switched back on.
	if !*p.cache.Snapshot().MetadataSettings().AcoustID {
		return ""
	}
	key, err := p.cache.AcoustIDAPIKey()
	if err != nil {
		p.logger.Warn("acoustid key could not be opened; the fingerprint rung stays disabled", "error", err)
		return ""
	}
	return string(key)
}

// musicBrainzProvider builds ADR-29's provider on the process's one ADR-13 registry,
// where a failing source counts against only its own bucket.
func (p *process) musicBrainzProvider() musicbrainz.Provider {
	sources := p.config.Metadata.MusicBrainz
	return musicbrainz.New(musicbrainz.Config{
		Sources: p.musicBrainzSources(sources), LiveOrder: p.liveMetadataSourceOrder,
		Cache: metacache.New(p.metadata), Clock: clock.NewSystem(),
		Logger: p.logger, RefreshAfter: sources.RefreshAfter.AsDuration(),
	})
}

// liveMetadataSourceOrder answers the configured order on every lookup rather
// than at construction: the jobs hold one Provider for the life of the
// process, so a captured order would stay stale until a restart.
func (p *process) liveMetadataSourceOrder() []string {
	return p.cache.Snapshot().MetadataSettings().SourceOrder
}

// musicBrainzSources builds every source config.yml enables. Which of them are
// tried, and in what order, is liveMetadataSourceOrder's job -- this only
// decides what exists to choose from.
func (p *process) musicBrainzSources(sources config.MusicBrainzConfig) []musicbrainz.Source {
	wall := clock.NewSystem()
	guard := httpx.NewGuard(nil, nil, nil)
	budget := p.musicBrainzBudget()
	agent := httpx.UserAgent(p.build.Version)
	newClient := func(provider string) *httpx.Client {
		return httpx.NewClient(wall, guard, budget.limiter, budget.breaker, httpx.ClientConfig{
			Provider: provider, UserAgent: agent, Proxy: p.liveProxyPolicy(),
		})
	}
	var ordered []musicbrainz.Source
	if sources.BrainzMashEnabled {
		client := httpx.NewClient(wall, guard, budget.limiter, budget.breaker, httpx.ClientConfig{
			Provider: musicbrainz.BrainzMashName, Proxy: p.liveProxyPolicy(),
			LiveUserAgent: func() string { return p.cache.Snapshot().MetadataSettings().BrainzMashUserAgent },
		})
		ordered = append(ordered, musicbrainz.NewBrainzMash(client, sources.BrainzMashBaseURL))
	}
	if sources.ProxyEnabled {
		ordered = append(ordered, musicbrainz.NewProxy(newClient(musicbrainz.ProxyName), sources.ProxyBaseURL))
	}
	return append(ordered, musicbrainz.NewDirect(newClient(musicbrainz.DirectName), sources.BaseURL))
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
		client := httpx.NewClient(wall, connectionGuard(baseURL), limiter, breaker, httpx.ClientConfig{
			Provider: "arr", UserAgent: agent, Proxy: p.liveProxyPolicy(), Timeout: 60 * time.Second,
		})
		return arrimport.NewLidarrClient(client, baseURL, apiKey)
	}
}

// listProwlarr reads a Prowlarr's indexer list; the guard admits Prowlarr's own host, as for an indexer.
func (p *process) listProwlarr(ctx context.Context, baseURL, apiKey string) ([]indexer.ProwlarrIndexer, error) {
	wall := clock.NewSystem()
	client := httpx.NewClient(wall, connectionGuard(baseURL), httpx.NewLimiter(wall, httpx.Config{}, nil),
		httpx.NewBreaker(wall, httpx.BreakerConfig{}), httpx.ClientConfig{
			Provider: "prowlarr", UserAgent: httpx.UserAgent(p.build.Version), Proxy: p.liveProxyPolicy(),
			Timeout: 30 * time.Second,
		})
	return indexer.ListProwlarr(ctx, client, baseURL, apiKey)
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

	return func(indexerID int64, baseURL, apiKey string, egress settings.IndexerEgress) indexer.Client {
		client := httpx.NewClient(wall, connectionGuard(baseURL), limiter, breaker, httpx.ClientConfig{
			Provider: "indexer:" + strconv.FormatInt(indexerID, 10), UserAgent: agent,
			Proxy: p.indexerLiveProxyPolicy(egress),
		})
		return indexer.NewClient(client, baseURL, apiKey)
	}
}

// downloaderClientFactory builds adapters.DownloaderHTTPFactory, one
// rate-limit/breaker bucket per connection id -- gentle by default (a
// downloader is polled, not fanned out to), the global egress proxy applies
// like any other outbound client (ADR-47 is indexer-specific, not this).
func (p *process) downloaderClientFactory() adapters.DownloaderHTTPFactory {
	wall := clock.NewSystem()
	limiter := httpx.NewLimiter(wall, httpx.Config{
		Defaults: httpx.Limits{MinInterval: 200 * time.Millisecond},
	}, nil)
	breaker := httpx.NewBreaker(wall, httpx.BreakerConfig{})
	agent := httpx.UserAgent(p.build.Version)

	return func(downloaderID int64, baseURL string) *httpx.Client {
		return httpx.NewClient(wall, connectionGuard(baseURL), limiter, breaker, httpx.ClientConfig{
			Provider: "downloader:" + strconv.FormatInt(downloaderID, 10), UserAgent: agent,
			Proxy: p.liveProxyPolicy(),
		})
	}
}

// connectionGuard is the SSRF guard for a connection an administrator configured: its own host may
// be on the LAN (Prowlarr, SABnzbd, Lidarr usually are), every other destination -- a redirect
// included -- is still classified. The *Arr sync needed this first; indexers and clients never had it.
func connectionGuard(baseURL string) *httpx.Guard {
	var allowedHosts []string
	if parsed, err := url.Parse(baseURL); err == nil && parsed.Hostname() != "" {
		allowedHosts = []string{parsed.Hostname()}
	}
	return httpx.NewGuard(nil, nil, allowedHosts)
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

// theAudioDB is TheAudioDB's client, on its own rate bucket, with the own key when one is stored.
func (p *process) theAudioDB() theaudiodb.Client {
	wall := clock.NewSystem()
	client := httpx.NewClient(wall, httpx.NewGuard(nil, nil, nil),
		httpx.NewLimiter(wall, httpx.Config{Defaults: httpx.Limits{MinInterval: 500 * time.Millisecond}}, nil),
		httpx.NewBreaker(wall, httpx.BreakerConfig{}), httpx.ClientConfig{
			Provider: "theaudiodb", UserAgent: httpx.UserAgent(p.build.Version), Proxy: p.liveProxyPolicy(),
		})
	return theaudiodb.New(client, func() string {
		if key, err := p.cache.TheAudioDBAPIKey(); err == nil && key != "" {
			return key.Reveal()
		}
		return theaudiodb.PublicKey
	})
}

// theAudioDBArtist is music.ArtistArt over TheAudioDB, asking nothing while it is switched off.
func (p *process) theAudioDBArtist(client theaudiodb.Client) music.ArtistArt {
	return func(ctx context.Context, artistMBID string) (string, string, error) {
		if !*p.cache.Snapshot().MetadataSettings().TheAudioDB {
			return "", "", nil
		}
		artist, err := client.Artist(ctx, artistMBID)
		return artist.Photo, artist.Biography, err
	}
}

// theAudioDBCover answers a release group's cover from TheAudioDB, in the artwork job's terms.
func theAudioDBCover(client theaudiodb.Client) func(ctx context.Context, releaseGroupMBID string) (string, error) {
	return func(ctx context.Context, releaseGroupMBID string) (string, error) {
		cover, err := client.AlbumCover(ctx, releaseGroupMBID)
		if errors.Is(err, theaudiodb.ErrNotFound) {
			return "", musicbrainz.ErrNoCoverArt
		}
		return cover, err
	}
}

// tvService is the process's one TV service: Skyhook and TVmaze each on their own ADR-13 bucket, TVmaze at its
// published 20 calls per 10 seconds.
func (p *process) tvService() tv.Service {
	p.tvOnce.Do(func() {
		wall := clock.NewSystem()
		guard := httpx.NewGuard(nil, nil, nil)
		limiter := httpx.NewLimiter(wall, httpx.Config{Providers: map[string]httpx.Limits{
			"skyhook": {MinInterval: 250 * time.Millisecond},
			"tvmaze":  {MinInterval: 500 * time.Millisecond},
			"tmdb":    {MinInterval: 50 * time.Millisecond},
			"tvdb":    {MinInterval: 100 * time.Millisecond},
			"anilist": {MinInterval: 2 * time.Second},
		}}, nil)
		client := func(name string) *httpx.Client {
			return httpx.NewClient(wall, guard, limiter, httpx.NewBreaker(wall, httpx.BreakerConfig{}), httpx.ClientConfig{
				Provider: name, UserAgent: httpx.UserAgent(p.build.Version), Proxy: p.liveProxyPolicy(),
			})
		}
		p.tmdbShared = tmdb.New(client("tmdb"), p.tmdbCredential, "")
		p.tvdbShared = tvdb.New(client("tvdb"), p.tvdbKey, nil, wall.Now)
		p.anilistShared = anilist.New(client("anilist"))
		p.skyhookShared, p.tvmazeShared = skyhook.New(client("skyhook"), "en"), tvmaze.New(client("tvmaze"))
		p.tvShared = tv.Service{
			Skyhook: p.skyhookShared, TVmaze: p.tvmazeShared, TMDB: p.tmdbShared, AniList: p.anilistShared,
			Priority: func() []string { return p.cache.Snapshot().MetadataSettings().TVSources },
			Series:   library.NewSeries(p.library), Reader: library.NewSeriesReader(p.library), Desired: library.NewDesired(p.library), Clock: wall, Logger: p.logger,
		}
	})
	return p.tvShared
}

// seriesCatalog is the TV screens' data source.
func (p *process) seriesCatalog() adapters.SeriesCatalog {
	return adapters.SeriesCatalog{
		Service: p.tvService(), Reader: library.NewSeriesReader(p.library), Desired: library.NewDesired(p.library),
		Categories: adapters.Categories{Cache: p.cache}, DefaultProfileID: p.defaultQualityProfileID, Clock: clock.NewSystem(),
		Remover: p.fileRemover(),
	}
}

// seasonSearch is the TV seasons' interactive search and send.
func (p *process) seasonSearch() adapters.SeasonSearch {
	grab := p.tvGrab()
	return adapters.SeasonSearch{
		Acquire: grab, Reader: library.NewSeriesReader(p.library), Desired: library.NewDesired(p.library),
		Conns: p.tvIndexerConns, Clock: clock.NewSystem(),
		Views: adapters.CandidateSearch{Search: p.musicSearch(), Dispatch: p.musicDispatch(), Clock: clock.NewSystem()},
	}
}

// tmdbCredential is the user's own TMDB key, empty until they store one.
func (p *process) tmdbCredential() string {
	key, err := p.cache.TMDBAPIKey()
	if err != nil {
		p.logger.Warn("tmdb key could not be opened; TMDB stays off", "error", err)
		return ""
	}
	return string(key)
}

// tvdbKey is the user's own TheTVDB key, else Solarr's built-in project key.
func (p *process) tvdbKey() string {
	if key, err := p.cache.TVDBAPIKey(); err == nil && key != "" {
		return string(key)
	}
	return tvdb.ProjectKey
}

// tvProviders is apiserver.TVProviders over the process's four TV sources.
func (p *process) tvProviders() adapters.TVProviders {
	p.tvService()
	snapshot := func() *settings.Snapshot { return p.cache.Snapshot() }
	return adapters.TVProviders{Probes: []adapters.TVProviderProbe{
		{
			Info: apiserver.TVProviderInfo{ID: "skyhook", Name: "Skyhook (Sonarr's TheTVDB proxy)", Key: "not_needed",
				Adds: "Series, episodes in TheTVDB's aired order, cast and artwork. The numbering basis."},
			Ping: func(ctx context.Context) error { _, err := p.skyhookShared.Search(ctx, "test"); return err },
		},
		{
			Info: apiserver.TVProviderInfo{ID: "tvmaze", Name: "TVmaze", Key: "not_needed",
				Adds: "Air times with the network's time zone, aliases and web channels."},
			Ping: func(ctx context.Context) error { _, err := p.tvmazeShared.Show(ctx, 1); return err },
		},
		{
			Info: apiserver.TVProviderInfo{ID: "tmdb", Name: "TMDB", Key: "missing",
				Adds: "Cast, crew and guest stars, artwork, keywords, networks and alternative titles. Needs your own free key."},
			Keyed: func() string {
				if snapshot().TMDBConfigured() {
					return "stored"
				}
				return "missing"
			},
			Ping: func(ctx context.Context) error {
				err := p.tmdbShared.Ping(ctx)
				if errors.Is(err, tmdb.ErrBadKey) {
					return errors.New("TMDB refused the key")
				}
				return err
			},
		},
		{
			Info: apiserver.TVProviderInfo{ID: "anilist", Name: "AniList", Key: "not_needed",
				Adds: "The titles anime is released under (romaji, English, native, synonyms) and banners; only for anime."},
			Ping: func(ctx context.Context) error { return p.anilistShared.Ping(ctx) },
		},
		{
			Info: apiserver.TVProviderInfo{ID: "tvdb", Name: "TheTVDB", Key: "built_in",
				Adds: "Direct access to TheTVDB: every ordering (DVD, absolute), characters and artwork."},
			Keyed: func() string {
				if snapshot().TVDBConfigured() {
					return "stored"
				}
				return "built_in"
			},
			Ping: func(ctx context.Context) error { return p.tvdbShared.Ping(ctx) },
		},
	}}
}

// seriesPeople is the TV people and network screens' data source.
func (p *process) seriesPeople() adapters.SeriesPeople {
	p.tvService()
	return adapters.SeriesPeople{
		Reader: library.NewSeriesReader(p.library), Persons: library.NewPeople(p.library),
		Subscriptions: library.NewSubscriptions(p.library), TMDB: p.tmdbShared, Clock: clock.NewSystem(),
		Preview: p.followJob().Preview,
		EnqueueFollow: func(ctx context.Context, subscriptionID int64) error {
			_, err := tv.EnqueueFollow(ctx, p.queue, subscriptionID)
			return err
		},
	}
}

// remoteImages is the provider picture cache, on its own rate bucket so a wall of posters cannot starve the metadata calls.
func (p *process) remoteImages() *remoteimage.Cache {
	wall := clock.NewSystem()
	limiter := httpx.NewLimiter(wall, httpx.Config{Providers: map[string]httpx.Limits{"images": {MinInterval: 100 * time.Millisecond}}}, nil)
	client := httpx.NewClient(wall, httpx.NewGuard(nil, nil, nil), limiter, httpx.NewBreaker(wall, httpx.BreakerConfig{}), httpx.ClientConfig{
		Provider: "images", UserAgent: httpx.UserAgent(p.build.Version), Proxy: p.liveProxyPolicy(),
	})
	return &remoteimage.Cache{HTTP: client, FS: p.fsys, Dir: filepath.Join(p.config.Paths.CacheDir, "images"), Hosts: remoteimage.DefaultHosts}
}

// unidentifiedTV places queued TV files by a person's choice.
func (p *process) unidentifiedTV() adapters.UnidentifiedTV {
	queue := library.NewUnidentified(p.library)
	return adapters.UnidentifiedTV{
		Identifier: tv.Identifier{
			Service: p.tvService(), Unidentified: queue, Clock: clock.NewSystem(), DefaultProfileID: p.defaultQualityProfileID,
			Categories: adapters.Categories{Cache: p.cache},
		},
		Unidentified: queue, Cache: p.cache, Exclusions: p.seriesCatalog(),
	}
}
