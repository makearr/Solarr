package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/makearr/Solarr/internal/media/adult"
	"github.com/makearr/Solarr/internal/metadata/stashbox"
	"github.com/makearr/Solarr/internal/metadata/theporndb"
	"github.com/makearr/Solarr/internal/metadata/whisparr"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/indexer/flaresolverr"
	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/importing/arrimport"
	"github.com/makearr/Solarr/internal/media/book"
	"github.com/makearr/Solarr/internal/media/movie"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/metadata/acoustid"
	"github.com/makearr/Solarr/internal/metadata/anidb"
	"github.com/makearr/Solarr/internal/metadata/anilist"
	"github.com/makearr/Solarr/internal/metadata/audible"
	"github.com/makearr/Solarr/internal/metadata/fanart"
	"github.com/makearr/Solarr/internal/metadata/fantlab"
	"github.com/makearr/Solarr/internal/metadata/googlebooks"
	"github.com/makearr/Solarr/internal/metadata/hardcover"
	"github.com/makearr/Solarr/internal/metadata/internetarchive"
	"github.com/makearr/Solarr/internal/metadata/itunes"
	"github.com/makearr/Solarr/internal/metadata/kitsu"
	"github.com/makearr/Solarr/internal/metadata/mdblist"
	"github.com/makearr/Solarr/internal/metadata/metacache"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
	"github.com/makearr/Solarr/internal/metadata/myanimelist"
	"github.com/makearr/Solarr/internal/metadata/omdb"
	"github.com/makearr/Solarr/internal/metadata/openlibrary"
	"github.com/makearr/Solarr/internal/metadata/radarrmeta"
	"github.com/makearr/Solarr/internal/metadata/skyhook"
	"github.com/makearr/Solarr/internal/metadata/theaudiodb"
	"github.com/makearr/Solarr/internal/metadata/thexem"
	"github.com/makearr/Solarr/internal/metadata/tmdb"
	"github.com/makearr/Solarr/internal/metadata/tvdb"
	"github.com/makearr/Solarr/internal/metadata/tvmaze"
	"github.com/makearr/Solarr/internal/metadata/wikidata"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
	"github.com/makearr/Solarr/internal/platform/httpx"
	"github.com/makearr/Solarr/internal/platform/remoteimage"
	"github.com/makearr/Solarr/internal/providerdef"
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

// wikidataFetcher builds ADR-8's biography lookup with two hosts in two ADR-13 buckets, separate from MusicBrainz's so a Wikidata outage never trips its breaker.
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
		Sources: p.musicBrainzSources(sources), LiveOrder: p.liveMetadataSourceOrder, LiveBootlegs: p.liveIncludesBootlegOnly,
		Cache: metacache.New(p.metadata).Counting(&p.metadataLookups), Clock: clock.NewSystem(),
		Logger: p.logger, RefreshAfter: sources.RefreshAfter.AsDuration(),
	})
}

// liveMetadataSourceOrder answers the library's configured order on every lookup rather than at construction: the jobs
// hold one Provider for the life of the process, so a captured order would stay stale until a restart.
func (p *process) liveMetadataSourceOrder(library int64) []string {
	return p.cache.Snapshot().MusicSourceOrder(library)
}

// liveIncludesBootlegOnly answers the library's switch on every discography lookup, like liveMetadataSourceOrder.
func (p *process) liveIncludesBootlegOnly(library int64) bool {
	return p.cache.Snapshot().IncludesBootlegOnly(library)
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

// arrRequestTimeout is long because a Whisparr scene list is one response of over a hundred megabytes.
const arrRequestTimeout = 20 * time.Minute

// arrClientFactory builds adapters.LidarrClientFactory: one gentle bucket per *Arr host (2 req/s), since the far end is someone's busy server.
// The guard allows baseURL's host: an admin-entered, persisted *Arr connection is the LAN opt-out httpx.Guard allows (ADR-52), unlike an attacker-controlled URL.
func (p *process) arrClientFactory() adapters.LidarrClientFactory {
	return func(baseURL, apiKey string) arrimport.LidarrClient {
		client := p.providerClient(providerArrPrefix+arrHost(baseURL), connectionGuard(baseURL), httpx.ClientConfig{Timeout: arrRequestTimeout})
		return arrimport.NewLidarrClient(client, baseURL, apiKey)
	}
}

// arrHost names an instance's bucket; an unparsable address shares one bucket rather than failing the call.
func arrHost(baseURL string) string {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" {
		return "unknown"
	}
	return parsed.Host
}

// listProwlarr reads a Prowlarr's indexer list; the guard admits Prowlarr's own host, as for an indexer.
func (p *process) listProwlarr(ctx context.Context, baseURL, apiKey string) ([]indexer.ProwlarrIndexer, error) {
	client := p.providerClient(providerProwlarr, connectionGuard(baseURL), httpx.ClientConfig{Timeout: 30 * time.Second})
	return indexer.ListProwlarr(ctx, client, baseURL, apiKey)
}

// listProwlarrProxies reads a Prowlarr's indexer proxies, as listProwlarr reads its indexers.
func (p *process) listProwlarrProxies(ctx context.Context, baseURL, apiKey string) ([]indexer.ProwlarrProxy, error) {
	client := p.providerClient(providerProwlarr, connectionGuard(baseURL), httpx.ClientConfig{Timeout: 30 * time.Second})
	return indexer.ListProwlarrProxies(ctx, client, baseURL, apiKey)
}

// indexerLiveProxyPolicy resolves ADR-47's per-indexer egress proxy, falling back to ADR-58's global one; the SSRF guard is unaffected since Proxy only picks the tunnel, not the checked host.
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

// indexerClientFactory builds adapters.IndexerClientFactory with one rate-limit/breaker bucket per indexer id (ADR-37), created lazily by the registries, so parallel searches never burst one provider.
func (p *process) indexerClientFactory() adapters.IndexerHTTPFactory {
	return func(indexerID int64, baseURL string, egress settings.IndexerEgress) *httpx.Client {
		guard := connectionGuard(baseURL)
		config := httpx.ClientConfig{
			Proxy: p.indexerLiveProxyPolicy(egress), Jar: p.indexerJar(indexerID),
			// A 200 that says "request limit reached", or that the key is refused, is a failure to back off from; a limit also slows the pace.
			AnswerFailed: func(start []byte) bool {
				limited := indexer.LimitReached(start)
				if limited && p.indexerPacing != nil {
					p.indexerPacing.limitHit(indexerID)
				}
				return limited || indexer.AccountRefused(start)
			},
		}
		name := providerIndexerPrefix + strconv.FormatInt(indexerID, 10)
		if egress.FlareSolverrURL != "" {
			config.Transport = p.flareSolverrTransport(guard, egress.FlareSolverrURL, config.Proxy)
		} else {
			config.Transport = p.pooledTransport(name, baseURL, egress.ProxyURL, guard, config.Proxy)
		}
		return p.providerClient(name, guard, config)
	}
}

// flareSolverrTransport reaches the site direct and FlareSolverr (whose own host the guard allows) when a challenge stops it.
func (p *process) flareSolverrTransport(site *httpx.Guard, solverURL string, proxy httpx.LiveProxyPolicy) http.RoundTripper {
	return &flaresolverr.Transport{
		Direct: httpx.NewTransport(site, proxy), Solver: httpx.NewTransport(connectionGuard(solverURL), nil), SolverURL: solverURL,
		CheckDestination: site.CheckDestination,
	}
}

// allowancePerHour is the owner's stated limit, else the one the tracker's own rules set.
func allowancePerHour(found settings.Indexer) int64 {
	if stated := found.QueriesPerHour(); stated > 0 {
		return stated
	}
	return indexer.DefaultQueriesPerHour(found.Impl)
}

// indexerDriver builds one configured indexer on its implementation; known is false for one Solarr has none of.
func (p *process) indexerDriver(ctx context.Context, found settings.Indexer, apiKey string) (indexer.Driver, bool) {
	if p.indexerPacing != nil {
		p.indexerPacing.setLimit(found.ID, allowancePerHour(found))
	}
	deps := adapters.IndexerDeps{HTTP: p.indexerClientFactory()(found.ID, found.BaseURL, p.cache.ResolveEgress(ctx, found)), Clock: clock.NewSystem(), Specs: p.specs}
	if p.indexerPacing != nil && found.Impl == indexer.ImplSpec {
		if one, ok := p.specs.Specs().Get(found.Definition()); ok {
			p.indexerPacing.setFloor(found.ID, time.Duration(one.RequestDelay*float64(time.Second)))
		}
	}
	if p.indexerPacing != nil && found.Impl != indexer.ImplSpec && p.definitions != nil {
		p.indexerPacing.setFloor(found.ID, descriptorRequestDelay(p.definitions.Catalog(), found.Impl))
	}
	return adapters.NewIndexer(found.Impl, deps, indexer.Connection{BaseURL: found.BaseURL, APIKey: apiKey, Definition: found.Definition()})
}

// descriptorRequestDelay is the gap a custom descriptor of a compiled indexer kind asks between requests; none or a bad value is zero.
func descriptorRequestDelay(catalog *providerdef.Catalog, impl string) time.Duration {
	delay, err := time.ParseDuration(catalog.Tuning(providerdef.KindIndexer, descriptorKey(impl)).RequestDelay)
	if err != nil || delay < 0 {
		return 0
	}
	return delay
}

// downloaderClientFactory builds adapters.DownloaderHTTPFactory with one gentle bucket per connection id, since a downloader is polled, and the global egress proxy applies (ADR-47 is indexer-specific).
func (p *process) downloaderClientFactory() adapters.DownloaderHTTPFactory {
	return func(downloaderID int64, baseURL string) *httpx.Client {
		name, guard, proxy := providerDownloaderPrefix+strconv.FormatInt(downloaderID, 10), connectionGuard(baseURL), p.liveProxyPolicy()
		return p.providerClient(name, guard, httpx.ClientConfig{Proxy: proxy, Transport: p.pooledTransport(name, baseURL, "", guard, proxy)})
	}
}

// pooledTransport is the connection's transport, kept between calls: the address and the connection's own proxy are
// what it captures, so a change of either builds a new one.
func (p *process) pooledTransport(name, baseURL, proxyURL string, guard *httpx.Guard, proxy httpx.LiveProxyPolicy) *http.Transport {
	return p.transports.For(name, baseURL+"|"+proxyURL, func() *http.Transport { return httpx.NewTransport(guard, proxy) })
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
		if errors.Is(err, theaudiodb.ErrNotFound) {
			return "", "", nil
		}
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

// tvdbClient is the process's one TheTVDB client, made with the TV service.
func (p *process) tvdbClient() tvdb.Client {
	p.tvService()
	return p.tvdbShared
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
		p.xemShared = thexem.New(client(providerTheXEM), metacache.New(p.metadata).Counting(&p.metadataLookups), wall)
		p.tvShared = tv.Service{
			Skyhook: p.skyhookShared, TVmaze: p.tvmazeShared, TMDB: p.tmdbShared, AniList: p.anilistShared, Wikidata: p.wikidataFacts(),
			Local: p.localMetadata(), Records: p.tvRecords,
			Kitsu:  kitsu.New(p.providerClient(providerKitsu, nil, httpx.ClientConfig{})),
			MAL:    myanimelist.New(p.providerClient(providerMAL, nil, httpx.ClientConfig{}), p.providerKey(settings.TVSourceMAL)),
			AniDB:  anidb.New(p.providerClient(providerAniDB, nil, httpx.ClientConfig{}), p.providerKey(settings.TVSourceAniDB)),
			IMDb:   p.imdbData(),
			OMDb:   omdb.New(p.providerClient(providerOMDb, nil, httpx.ClientConfig{}), p.providerKey(settings.MovieSourceOMDb)),
			Fanart: fanart.New(p.providerClient(providerFanart, nil, httpx.ClientConfig{}), p.providerKey(settings.MovieSourceFanart)),
			Scene:  sceneSource{client: p.xemShared, enabled: func() bool { return p.cache.Snapshot().ProviderEnabled(settings.SourceTheXEM) }},
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
			AltTitleLanguages: func(categoryID int64) []string {
				snapshot := p.cache.Snapshot()
				category, found := snapshot.Category(categoryID)
				if !found || category.LanguageProfileID == nil {
					return nil
				}
				profile, found := snapshot.LanguageProfile(*category.LanguageProfileID)
				if !found {
					return nil
				}
				languages := append(slices.Clone(profile.AudioRequired), profile.AudioPreferred...)
				slices.Sort(languages)
				return slices.Compact(languages)
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
			Wikidata: p.wikidataFacts(), Records: p.movieRecords,
			MDBList: mdblist.New(p.providerClient(providerMDBList, nil, httpx.ClientConfig{}), p.providerKey(settings.MovieSourceMDBList)),
			OMDb:    omdb.New(p.providerClient(providerOMDb, nil, httpx.ClientConfig{}), p.providerKey(settings.MovieSourceOMDb)),
			IMDb:    p.imdbData(),
			Fanart:  fanart.New(p.providerClient(providerFanart, nil, httpx.ClientConfig{}), p.providerKey(settings.MovieSourceFanart)),
			TVDB:    p.tvdbShared,
			Local:   p.localMetadata(),
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
				Format: each, OpenLibrary: openLibrary, GoogleBooks: googleBooks, Audible: catalog, ITunes: appleBooks, FantLab: fantLab, Hardcover: hardcoverClient, Archive: internetarchive.New(p.providerClient(providerInternetArchive, nil, httpx.ClientConfig{})), Wikidata: p.wikidataFacts(), Local: p.localMetadata(), Records: p.bookRecords,
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
		Remover: p.fileRemover(), AfterAdd: p.lookForCrossoverPartsAgain,
	}
}

// lookForCrossoverPartsAgain has the next franchise pass compare every family of series once more and runs one at once.
func (p *process) lookForCrossoverPartsAgain(ctx context.Context) {
	if err := library.NewCrossovers(p.library).ForgetFamilyChecks(ctx); err != nil {
		p.logger.Warn("crossover families not reset", "error", err)
		return
	}
	if _, err := p.enqueueFranchisePass(ctx); err != nil {
		p.logger.Warn("franchise pass not queued", "error", err)
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
		{
			Info: apiserver.TVProviderInfo{ID: "thexem", Name: "TheXEM", Key: "not_needed",
				Adds: "Scene numbering: searches ask for the numbers releases carry (S02E05 where TheTVDB says S01E25) and either is accepted."},
			Ping: func(ctx context.Context) error { return p.xemShared.Ping(ctx) },
		},
	}}
}

// sceneSource is TheXEM, mapping nothing while it is switched off (the next refresh then drops the scene numbers).
type sceneSource struct {
	client  thexem.Client
	enabled func() bool
}

func (s sceneSource) Mappings(ctx context.Context, tvdbID int64) ([]thexem.Mapping, error) {
	if !s.enabled() {
		return nil, nil
	}
	return s.client.Mappings(ctx, tvdbID)
}

func (s sceneSource) Names(ctx context.Context, tvdbID int64) ([]string, error) {
	if !s.enabled() {
		return nil, nil
	}
	return s.client.Names(ctx, tvdbID)
}

// remoteImages is the provider picture cache, on its own rate bucket so a wall of posters cannot starve the metadata calls.
func (p *process) remoteImages() *remoteimage.Cache {
	client := p.providerClient(providerImages, nil, httpx.ClientConfig{})
	return &remoteimage.Cache{HTTP: client, FS: p.fsys, Dir: p.imagesDir(), CoverDir: p.config.Paths.MediaCoverDir, Clock: clock.NewSystem(), Hosts: remoteimage.DefaultHosts, Local: localImages{p: p}, Lookups: &p.imageLookups}
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
			config := httpx.ClientConfig{AnswerFailed: stashbox.AnswerFailed}
			return stashbox.New(p.providerClient(provider, nil, config), endpoint, p.providerKey(provider)).
				WithFingerprintBucket(p.providerClient(provider+providerFingerprintSuffix, nil, config))
		}
		p.adultShared = adult.Service{
			Memo:     adult.NewSearchMemo(clock.NewSystem()),
			Local:    p.localMetadata(),
			Whisparr: whisparr.New(p.providerClient(providerWhisparr, nil, httpx.ClientConfig{Timeout: whisparrTimeout})),
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
		Studios:    &adult.StudioAdds{Organisations: library.NewOrganisations(p.library), Follows: library.NewSubscriptions(p.library), Now: func() time.Time { return clock.NewSystem().Now() }},
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

// mdblistLists is MDBList on the bucket the movie ratings use, for import lists.
func (p *process) mdblistLists() mdblist.Client {
	return mdblist.New(p.providerClient(providerMDBList, nil, httpx.ClientConfig{}), p.providerKey(settings.MovieSourceMDBList))
}

// indexerJar is the cookie jar of one indexer, kept for the life of the process so a sign-in outlasts a search; an
// indexer not saved yet (id 0) gets a fresh one.
func (p *process) indexerJar(indexerID int64) http.CookieJar {
	fresh, err := cookiejar.New(nil)
	if err != nil {
		return nil
	}
	if indexerID == 0 {
		return fresh
	}
	kept, _ := p.indexerJars.LoadOrStore(indexerID, fresh)
	return kept.(http.CookieJar)
}
