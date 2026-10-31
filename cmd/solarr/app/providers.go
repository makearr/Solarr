package app

import (
	"context"
	"errors"
	"net/url"
	"path/filepath"
	"strconv"
	"time"

	"github.com/makearr/Solarr/internal/media/adult"
	"github.com/makearr/Solarr/internal/metadata/stashbox"
	"github.com/makearr/Solarr/internal/metadata/theporndb"
	"github.com/makearr/Solarr/internal/metadata/whisparr"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/importing/arrimport"
	"github.com/makearr/Solarr/internal/media/book"
	"github.com/makearr/Solarr/internal/media/movie"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/metadata/acoustid"
	"github.com/makearr/Solarr/internal/metadata/anilist"
	"github.com/makearr/Solarr/internal/metadata/audible"
	"github.com/makearr/Solarr/internal/metadata/fanart"
	"github.com/makearr/Solarr/internal/metadata/fantlab"
	"github.com/makearr/Solarr/internal/metadata/googlebooks"
	"github.com/makearr/Solarr/internal/metadata/hardcover"
	"github.com/makearr/Solarr/internal/metadata/itunes"
	"github.com/makearr/Solarr/internal/metadata/mdblist"
	"github.com/makearr/Solarr/internal/metadata/metacache"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
	"github.com/makearr/Solarr/internal/metadata/omdb"
	"github.com/makearr/Solarr/internal/metadata/openlibrary"
	"github.com/makearr/Solarr/internal/metadata/radarrmeta"
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
	"github.com/makearr/Solarr/internal/service/importlist"
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
func (p *process) wikidataFetcher() wikidata.Fetcher {
	return wikidata.New(wikidata.Config{
		// The query service answers a batch of a library's series in seconds, but takes a minute when busy.
		WikidataClient:  p.providerClient(providerWikidata, nil, httpx.ClientConfig{Timeout: 90 * time.Second}),
		WikipediaClient: p.providerClient(providerWikipedia, nil, httpx.ClientConfig{}),
	})
}

// wikidataFacts is the process's one cache of what Wikidata says of films and series: refresh sweeps fill it in bulk.
func (p *process) wikidataFacts() *wikidata.Cached {
	p.wikidataOnce.Do(func() {
		p.wikidataShared = wikidata.NewCached(p.wikidataFetcher(), clock.NewSystem(), 6*time.Hour)
	})
	return p.wikidataShared
}

// acoustIDLookup answers the process's one AcoustID client: ADR-29's fingerprint
// rung, on its own ADR-13 bucket at the 3 req/s acoustid.org publishes.
func (p *process) acoustIDLookup() acoustid.Client {
	p.acoustIDOnce.Do(func() {
		p.acoustIDShared = acoustid.New(p.providerClient(acoustid.Name, nil, httpx.ClientConfig{}), "", p.acoustIDKey)
	})
	return p.acoustIDShared
}

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
		Cache: metacache.New(p.metadata).Counting(&p.metadataLookups), Clock: clock.NewSystem(),
		Logger: p.logger, RefreshAfter: sources.RefreshAfter.AsDuration(),
	})
}

// liveMetadataSourceOrder answers the library's configured order on every lookup rather than at construction: the jobs
// hold one Provider for the life of the process, so a captured order would stay stale until a restart.
func (p *process) liveMetadataSourceOrder(library int64) []string {
	return p.cache.Snapshot().MusicSourceOrder(library)
}

// musicBrainzSources builds every source config.yml enables. Which of them are
// tried, and in what order, is liveMetadataSourceOrder's job -- this only
// decides what exists to choose from.
func (p *process) musicBrainzSources(sources config.MusicBrainzConfig) []musicbrainz.Source {
	var ordered []musicbrainz.Source
	if sources.BrainzMashEnabled {
		client := p.providerClient(musicbrainz.BrainzMashName, nil, httpx.ClientConfig{
			LiveUserAgent: func() string { return p.cache.Snapshot().MetadataSettings().BrainzMashUserAgent },
		})
		ordered = append(ordered, musicbrainz.NewBrainzMash(client, sources.BrainzMashBaseURL))
	}
	if sources.ProxyEnabled {
		proxy := p.providerClient(musicbrainz.ProxyName, nil, httpx.ClientConfig{})
		ordered = append(ordered, musicbrainz.NewProxy(proxy, sources.ProxyBaseURL))
	}
	direct := p.providerClient(musicbrainz.DirectName, nil, httpx.ClientConfig{})
	return append(ordered, musicbrainz.NewDirect(direct, sources.BaseURL))
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
	return func(baseURL, apiKey string) arrimport.LidarrClient {
		client := p.providerClient(providerArr, connectionGuard(baseURL), httpx.ClientConfig{Timeout: 60 * time.Second})
		return arrimport.NewLidarrClient(client, baseURL, apiKey)
	}
}

// listProwlarr reads a Prowlarr's indexer list; the guard admits Prowlarr's own host, as for an indexer.
func (p *process) listProwlarr(ctx context.Context, baseURL, apiKey string) ([]indexer.ProwlarrIndexer, error) {
	client := p.providerClient(providerProwlarr, connectionGuard(baseURL), httpx.ClientConfig{Timeout: 30 * time.Second})
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
func (p *process) indexerClientFactory() adapters.IndexerHTTPFactory {
	return func(indexerID int64, baseURL string, egress settings.IndexerEgress) *httpx.Client {
		return p.providerClient(providerIndexerPrefix+strconv.FormatInt(indexerID, 10), connectionGuard(baseURL),
			httpx.ClientConfig{Proxy: p.indexerLiveProxyPolicy(egress)})
	}
}

// indexerDriver builds one configured indexer on its implementation; known is false for one Solarr has none of.
func (p *process) indexerDriver(found settings.Indexer, apiKey string) (indexer.Driver, bool) {
	deps := adapters.IndexerDeps{HTTP: p.indexerClientFactory()(found.ID, found.BaseURL, found.Egress()), Clock: clock.NewSystem()}
	return adapters.NewIndexer(found.Impl, deps, indexer.Connection{BaseURL: found.BaseURL, APIKey: apiKey})
}

// downloaderClientFactory builds adapters.DownloaderHTTPFactory, one
// rate-limit/breaker bucket per connection id -- gentle by default (a
// downloader is polled, not fanned out to), the global egress proxy applies
// like any other outbound client (ADR-47 is indexer-specific, not this).
func (p *process) downloaderClientFactory() adapters.DownloaderHTTPFactory {
	return func(downloaderID int64, baseURL string) *httpx.Client {
		return p.providerClient(providerDownloaderPrefix+strconv.FormatInt(downloaderID, 10), connectionGuard(baseURL), httpx.ClientConfig{})
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
	return p.providerClient(providerPublicIP, nil, httpx.ClientConfig{Timeout: 10 * time.Second})
}

// theAudioDB is TheAudioDB's client, on its own rate bucket, with the own key when one is stored.
func (p *process) theAudioDB() theaudiodb.Client {
	client := p.providerClient(providerTheAudioDB, nil, httpx.ClientConfig{})
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
		client := func(name string) *httpx.Client { return p.providerClient(name, nil, httpx.ClientConfig{}) }
		p.tmdbShared = tmdb.New(client(providerTMDB), p.tmdbCredential, "")
		p.tvdbShared = tvdb.New(client(providerTVDB), p.tvdbKey, nil, wall.Now)
		p.anilistShared = anilist.New(client(providerAniList))
		p.skyhookShared, p.tvmazeShared = skyhook.New(client(providerSkyhook), "en"), tvmaze.New(client(providerTVmaze))
		p.tvShared = tv.Service{
			Skyhook: p.skyhookShared, TVmaze: p.tvmazeShared, TMDB: p.tmdbShared, AniList: p.anilistShared, Wikidata: p.wikidataFacts(),
			Priority: func() []string {
				return p.cache.Snapshot().MetadataSourcesFor(settings.Category{MediaType: tv.MediaType})
			},
			PriorityFor: func(categoryID int64) []string {
				snapshot := p.cache.Snapshot()
				category, found := snapshot.Category(categoryID)
				if !found {
					return nil
				}
				return snapshot.MetadataSourcesFor(category)
			},
			Grace:     p.availabilityGrace,
			Orderings: p.tvdbShared,
			IgnoreNewSeasons: func(categoryID int64) bool {
				category, _ := p.cache.Snapshot().Category(categoryID)
				return category.FileManagement.IgnoreNewSeasons
			},
			KindOf: func(categoryID int64) string {
				category, _ := p.cache.Snapshot().Category(categoryID)
				return category.Kind
			},
			Series: library.NewSeries(p.library), Exclusions: library.NewExclusions(p.library), Reader: library.NewSeriesReader(p.library), Desired: library.NewDesired(p.library), Clock: wall, Logger: p.logger,
		}
	})
	return p.tvShared
}

// movieService is the process's one movie service: TMDB on the bucket TV already uses, Radarr's keyless service behind it.
func (p *process) movieService() movie.Service {
	p.movieOnce.Do(func() {
		p.tvService()
		proxy := radarrmeta.New(p.providerClient(providerRadarrMeta, nil, httpx.ClientConfig{}))
		p.movieShared = movie.Service{
			Wikidata: p.wikidataFacts(),
			MDBList:  mdblist.New(p.providerClient(providerMDBList, nil, httpx.ClientConfig{}), p.providerKey(settings.MovieSourceMDBList)),
			OMDb:     omdb.New(p.providerClient(providerOMDb, nil, httpx.ClientConfig{}), p.providerKey(settings.MovieSourceOMDb)),
			Fanart:   fanart.New(p.providerClient(providerFanart, nil, httpx.ClientConfig{}), p.providerKey(settings.MovieSourceFanart)),
			TVDB:     p.tvdbShared,
			Priority: func() []string {
				return p.cache.Snapshot().MetadataSourcesFor(settings.Category{MediaType: movie.MediaType})
			},
			PriorityFor: func(categoryID int64) []string {
				snapshot := p.cache.Snapshot()
				category, found := snapshot.Category(categoryID)
				if !found {
					return nil
				}
				return snapshot.MetadataSourcesFor(category)
			},
			TMDB: p.tmdbShared, Proxy: proxy,
			Series: library.NewSeries(p.library), Reader: library.NewSeriesReader(p.library), Desired: library.NewDesired(p.library),
			Franchises: library.NewFranchises(p.library), Exclusions: library.NewExclusions(p.library), Clock: clock.NewSystem(), Logger: p.logger,
		}
	})
	return p.movieShared
}

// bookService is the process's one book service of a format: Open Library, Google Books and Audible on buckets books
// and audiobooks share, steered by the one Wikidata cache.
func (p *process) bookService(format book.Format) book.Service {
	p.bookOnce.Do(func() {
		openLibrary := openlibrary.New(p.providerClient(providerOpenLibrary, nil, httpx.ClientConfig{}))
		googleBooks := googlebooks.New(p.providerClient(providerGoogleBooks, nil, httpx.ClientConfig{}), p.providerKey(settings.BookSourceGoogleBooks))
		audibleClient := p.providerClient(providerAudible, nil, httpx.ClientConfig{})
		catalog := audible.New(audibleClient, audibleClient, nil)
		appleBooks := itunes.New(p.providerClient(providerITunes, nil, httpx.ClientConfig{}))
		fantLab := fantlab.New(p.providerClient(providerFantLab, nil, httpx.ClientConfig{}))
		hardcoverClient := hardcover.New(p.providerClient(providerHardcover, nil, httpx.ClientConfig{}), p.providerKey(settings.BookSourceHardcover))
		p.bookShared = map[string]book.Service{}
		for _, each := range book.Formats {
			p.bookShared[each.MediaType] = book.Service{
				Format: each, OpenLibrary: openLibrary, GoogleBooks: googleBooks, Audible: catalog, ITunes: appleBooks, FantLab: fantLab, Hardcover: hardcoverClient, Wikidata: p.wikidataFacts(),
				PriorityFor: func(categoryID int64) []string {
					snapshot := p.cache.Snapshot()
					category, found := snapshot.Category(categoryID)
					if !found {
						return nil
					}
					return snapshot.MetadataSourcesFor(category)
				},
				Series: library.NewSeries(p.library), Reader: library.NewSeriesReader(p.library), Desired: library.NewDesired(p.library),
				Franchises: library.NewFranchises(p.library), Exclusions: library.NewExclusions(p.library), Clock: clock.NewSystem(), Logger: p.logger,
			}
		}
	})
	return p.bookShared[format.MediaType]
}

// bookCatalog is a book type's screens' data source.
func (p *process) bookCatalog(format book.Format) book.Catalog {
	return book.Catalog{
		Service: p.bookService(format), Categories: adapters.Categories{Cache: p.cache}, DefaultProfileID: p.defaultQualityProfileID,
		AllowAgain: func(ctx context.Context, provider, externalID string) error {
			return adapters.WorkExclusions{Exclusions: library.NewExclusions(p.library)}.Allow(ctx, format.MediaType, provider, externalID)
		},
	}
}

// movieCatalog is the movie screens' data source.
func (p *process) movieCatalog() movie.Catalog {
	return movie.Catalog{
		Service: p.movieService(), Categories: adapters.Categories{Cache: p.cache}, DefaultProfileID: p.defaultQualityProfileID,
		FranchiseStore: library.NewFranchises(p.library),
		AllowAgain: func(ctx context.Context, provider, externalID string) error {
			return adapters.WorkExclusions{Exclusions: library.NewExclusions(p.library)}.Allow(ctx, movie.MediaType, provider, externalID)
		},
	}
}

// seriesCatalog is the TV screens' data source.
func (p *process) seriesCatalog() adapters.SeriesCatalog {
	return adapters.SeriesCatalog{
		Service: p.tvService(), Reader: library.NewSeriesReader(p.library), Desired: library.NewDesired(p.library),
		Categories: adapters.Categories{Cache: p.cache}, DefaultProfileID: p.defaultQualityProfileID, Clock: clock.NewSystem(),
		Remover: p.fileRemover(),
	}
}

// providerKey answers, on every call, the owner's key for a provider: a key stored later is used without a restart.
func (p *process) providerKey(provider string) func() string {
	return func() string {
		key, err := p.cache.ProviderKey(provider)
		if err != nil {
			p.logger.Warn("provider key could not be opened", "provider", provider, "error", err)
			return ""
		}
		return string(key)
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

// remoteImages is the provider picture cache, on its own rate bucket so a wall of posters cannot starve the metadata calls.
func (p *process) remoteImages() *remoteimage.Cache {
	client := p.providerClient(providerImages, nil, httpx.ClientConfig{})
	return &remoteimage.Cache{HTTP: client, FS: p.fsys, Dir: filepath.Join(p.config.Paths.CacheDir, "images"), Hosts: remoteimage.DefaultHosts, Lookups: &p.imageLookups}
}

// unidentifiedTV places queued TV files by a person's choice.
func (p *process) unidentifiedTV() adapters.UnidentifiedTV {
	queue := library.NewUnidentified(p.library)
	return adapters.UnidentifiedTV{
		Identifier: tv.Identifier{
			Service: p.tvService(), Unidentified: queue, Clock: clock.NewSystem(), DefaultProfileID: p.defaultQualityProfileID,
			Categories: adapters.Categories{Cache: p.cache},
		},
		Unidentified: queue, Exclusions: adapters.WorkExclusions{Exclusions: library.NewExclusions(p.library)},
	}
}

// aniListClient is the process's one AniList client, built with the TV providers it shares a bucket with.
func (p *process) aniListClient() anilist.Client {
	p.tvService()
	return p.anilistShared
}

// adultService is the process's one adult service: Whisparr's server and the three stash-box servers, each on a bucket of
// its own, in each library's order.
func (p *process) adultService() adult.Service {
	p.adultOnce.Do(func() {
		server := func(provider, endpoint string) adult.Server {
			return stashbox.New(p.providerClient(provider, nil, httpx.ClientConfig{}), endpoint, p.providerKey(provider))
		}
		p.adultShared = adult.Service{
			Whisparr: whisparr.New(p.providerClient(providerWhisparr, nil, httpx.ClientConfig{})),
			Movies:   theporndb.New(p.providerClient(providerThePornDB, nil, httpx.ClientConfig{}), p.providerKey(settings.AdultSourceThePornDB)),
			Servers: map[string]adult.Server{
				adult.ProviderStashDB:   server(providerStashDB, stashbox.StashDBEndpoint),
				adult.ProviderThePornDB: server(providerThePornDB, stashbox.ThePornDBEndpoint),
				adult.ProviderFansDB:    server(providerFansDB, stashbox.FansDBEndpoint),
			},
			Sources: settings.DefaultAdultSources,
			PriorityFor: func(categoryID int64) []string {
				snapshot := p.cache.Snapshot()
				category, found := snapshot.Category(categoryID)
				if !found {
					return nil
				}
				return snapshot.MetadataSourcesFor(category)
			},
			Series: library.NewSeries(p.library), Reader: library.NewSeriesReader(p.library), Desired: library.NewDesired(p.library),
			Exclusions: library.NewExclusions(p.library), Clock: clock.NewSystem(), Logger: p.logger,
		}
	})
	return p.adultShared
}

// adultCatalog is the adult screens' data source.
func (p *process) adultCatalog() adult.Catalog {
	return adult.Catalog{
		Performers: &adult.PerformerAdds{People: library.NewPeople(p.library), Now: func() time.Time { return clock.NewSystem().Now() }},
		Service:    p.adultService(), Categories: adapters.Categories{Cache: p.cache}, DefaultProfileID: p.defaultQualityProfileID,
		AllowAgain: func(ctx context.Context, provider, externalID string) error {
			return adapters.WorkExclusions{Exclusions: library.NewExclusions(p.library)}.Allow(ctx, adult.MediaType, provider, externalID)
		},
	}
}

// stashServers are the stash-box clients an import list reads favourites from, by provider name.
func (p *process) stashServers() map[string]importlist.StashServer {
	servers := map[string]importlist.StashServer{}
	for name, server := range p.adultService().Servers {
		if listed, ok := server.(importlist.StashServer); ok {
			servers[name] = listed
		}
	}
	return servers
}
