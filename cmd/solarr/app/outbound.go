package app

import (
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
	"github.com/makearr/Solarr/internal/providerdef"
)

// Provider names of the clients built in this package; each is its own rate bucket.
const (
	providerWikidata        = "wikidata"
	providerWikipedia       = "wikipedia"
	providerWikimediaImage  = "wikimedia-image"
	providerTheAudioDB      = "theaudiodb"
	providerProwlarr        = "prowlarr"
	providerPublicIP        = "public-ip"
	providerImages          = "images"
	providerSkyhook         = "skyhook"
	providerTVmaze          = "tvmaze"
	providerTMDB            = "tmdb"
	providerTVDB            = "tvdb"
	providerAniList         = "anilist"
	providerKitsu           = "kitsu"
	providerInternetArchive = "internetarchive"
	providerTheXEM          = "thexem"
	providerRadarrMeta      = "radarr-meta"
	providerMDBList         = "mdblist"
	providerOMDb            = "omdb"
	providerIMDb            = "imdb"
	providerListenBrainz    = "listenbrainz"
	providerDeezer          = "deezer"
	providerLastFM          = "lastfm"
	providerDiscogs         = "discogs"
	providerSpotify         = "spotify"
	providerMAL             = "myanimelist"
	providerAniDB           = "anidb"
	providerFanart          = "fanart"
	providerOpenLibrary     = "openlibrary"
	providerGoogleBooks     = "googlebooks"
	providerHardcover       = "hardcover"
	providerITunes          = "itunes"
	providerAnimeIDs        = "animeids"
	providerLRCLIB          = "lrclib"
	providerFantLab         = "fantlab"
	providerBookBrainz      = "bookbrainz"
	providerWhisparr        = "whisparr"
	providerStashDB         = "stashdb"
	providerThePornDB       = "theporndb"
	providerFansDB          = "fansdb"
	// providerFingerprintSuffix names the stash-box fingerprint bucket: its breaker is separate from the search bucket's.
	providerFingerprintSuffix = "-fingerprint"
	// providerAudible is Audible's catalog and Audnexus together: Audnexus publishes the pace both are asked at.
	providerAudible = "audible"
	// Each indexer and each download client connection has a bucket of its own, named by its id.
	providerIndexerPrefix    = "indexer:"
	providerDownloaderPrefix = "downloader:"
	// Each *Arr instance has its own bucket, named by its host, so one instance being down pauses no other.
	providerArrPrefix = "arr:"
	// Each media server connection has its own bucket, named by its host.
	providerMediaServerPrefix = "mediaserver:"
)

// providerLimits is every provider's pace, declared once (ADR-13): the metadata providers' in settings.MetadataProviders,
// where a person may set their own, the rest here. A provider not listed is not paced.
var providerLimits = withMetadataPaces(map[string]httpx.Limits{
	// Wikipedia's REST API allows 200/s.
	providerWikipedia: {MinInterval: 100 * time.Millisecond},
	// LRCLIB asks for politeness only; one request a second.
	providerLRCLIB: {MinInterval: time.Second},
	// A wall of posters must not starve the metadata calls.
	providerImages: {MinInterval: 100 * time.Millisecond},
})

// whisparrTimeout is shorter than the shared one: a stuck search there held a scan worker for half a minute (2026-11-01).
const whisparrTimeout = 10 * time.Second

// metadataBuckets names the limiter bucket of a metadata provider whose client is named otherwise.
var metadataBuckets = map[string]string{settings.MovieSourceRadarr: providerRadarrMeta}

// bucketOf is the limiter bucket a metadata provider's requests go through.
func bucketOf(key string) string {
	if bucket, renamed := metadataBuckets[key]; renamed {
		return bucket
	}
	return key
}

// withMetadataPaces adds every metadata provider's shipped pace to limits.
func withMetadataPaces(limits map[string]httpx.Limits) map[string]httpx.Limits {
	for _, provider := range settings.MetadataProviders {
		if provider.PerMinute > 0 {
			limits[bucketOf(provider.Key)] = httpx.Limits{MinInterval: paceInterval(provider.PerMinute), MaxConcurrent: metadataConcurrency}
		}
	}
	return limits
}

// paceInterval is the gap between requests of a pace in requests a minute.
func paceInterval(perMinute int64) time.Duration { return time.Minute / time.Duration(perMinute) }

// livePace answers the pace a person set for the metadata provider behind a bucket (user, 2026-10-23).
func (p *process) livePace(bucket string) (time.Duration, bool) {
	if id, found := strings.CutPrefix(bucket, providerIndexerPrefix); found && p.indexerPacing != nil {
		if indexerID, err := strconv.ParseInt(id, 10, 64); err == nil {
			return p.indexerPacing.interval(indexerID)
		}
	}
	key := bucket
	for provider, renamed := range metadataBuckets {
		if renamed == bucket {
			key = provider
		}
	}
	if _, known := settings.MetadataProviderByKey(key); !known {
		return 0, false
	}
	perMinute, own := p.cache.Snapshot().ProviderPace(key)
	if !own || perMinute <= 0 {
		// A custom descriptor's pace comes next, then the shipped one.
		if p.definitions == nil {
			return 0, false
		}
		if tuned := p.definitions.Catalog().Tuning(providerdef.KindMetadata, descriptorKey(key)).PerMinute; tuned > 0 {
			return paceInterval(tuned), true
		}
		return 0, false
	}
	return paceInterval(perMinute), true
}

// providerFamilyLimits pace each connection of a kind on its own bucket: a search fans out to every indexer at
// once (ADR-37), a download client is polled.
var providerFamilyLimits = map[string]httpx.Limits{
	providerIndexerPrefix:    {MinInterval: 500 * time.Millisecond, MaxConcurrent: connectionConcurrency},
	providerDownloaderPrefix: {MinInterval: 200 * time.Millisecond, MaxConcurrent: connectionConcurrency},
	// Someone's own *Arr server, often busy with its own imports.
	providerArrPrefix: {MinInterval: 500 * time.Millisecond, MaxConcurrent: connectionConcurrency},
	// Someone's own media server, which is also being watched on.
	providerMediaServerPrefix: {MinInterval: 200 * time.Millisecond, MaxConcurrent: connectionConcurrency},
}

// connectionConcurrency is how many requests one indexer, download client or *Arr connection has in flight; the pace only
// spaces their starts, so a slow answer would otherwise pile up requests on a server that is already struggling.
const connectionConcurrency = 2

// metadataConcurrency is the same bound for one metadata provider.
const metadataConcurrency = 4

// outbound is the process's one rate-limit registry and one circuit breaker (ADR-13). A limiter built anywhere
// else is a second budget for the same provider, which is how MusicBrainz came to answer 503 (2026-09-17).
type outbound struct {
	limiter *httpx.Limiter
	breaker *httpx.Breaker
}

func (p *process) outbound() outbound {
	p.outboundOnce.Do(func() {
		wall := clock.NewSystem()
		p.outboundShared = outbound{
			limiter: httpx.NewLimiter(wall, httpx.Config{Providers: providerLimits, Prefixes: providerFamilyLimits, LivePace: p.livePace, LaneShares: p.laneShares}, nil),
			breaker: httpx.NewBreaker(wall, httpx.BreakerConfig{OnRecovered: p.noteProviderRecovered}),
		}
	})
	return p.outboundShared
}

// laneShares answers the weights a person set for the three request lanes, read at every decision.
func (p *process) laneShares() httpx.LaneShares {
	shares := p.cache.Snapshot().RequestShares()
	return httpx.LaneShares{User: float64(shares.User), Fresh: float64(shares.Fresh), Background: float64(shares.Background)}
}

// providerClient builds a client on the shared budget. The provider name is its bucket; a nil guard is the default
// SSRF guard, and the global egress proxy and the user agent apply unless config sets its own.
func (p *process) providerClient(provider string, guard *httpx.Guard, config httpx.ClientConfig) *httpx.Client {
	shared := p.outbound()
	if guard == nil {
		guard = httpx.NewGuard(nil, nil, nil)
	}
	config.Provider = provider
	if p.timings != nil {
		config.Meter = p.timings
	}
	config.RejectedKeyOpens = !strings.HasPrefix(provider, providerIndexerPrefix) && !strings.HasPrefix(provider, providerDownloaderPrefix) &&
		!strings.HasPrefix(provider, providerArrPrefix) && !strings.HasPrefix(provider, providerMediaServerPrefix) && provider != providerProwlarr && provider != providerWhisparr
	_, metadata := settings.MetadataProviderByKey(provider)
	config.RetryTransient = metadata && provider != providerWhisparr
	if config.UserAgent == "" && config.LiveUserAgent == nil {
		config.UserAgent = httpx.UserAgent(p.build.Version)
	}
	if config.Proxy == nil {
		config.Proxy = p.liveProxyPolicy()
	}
	return httpx.NewClient(clock.NewSystem(), guard, shared.limiter, shared.breaker, config)
}

// tvSourcesPaused names the TV providers whose circuit is not closed, in the order lookups try them.
func (p *process) tvSourcesPaused() []string {
	names := map[string]string{
		providerSkyhook: "Skyhook", providerTVmaze: "TVmaze", providerTMDB: "TMDB", providerTVDB: "TheTVDB", providerAniList: "AniList",
	}
	var paused []string
	for _, provider := range []string{providerSkyhook, providerTVmaze, providerTMDB, providerTVDB, providerAniList} {
		if p.outbound().breaker.State(provider).State != httpx.BreakerClosed {
			paused = append(paused, names[provider])
		}
	}
	return paused
}

// sceneNumberingDown says TheXEM's circuit is open, so new scene numbers wait until it answers again.
func (p *process) sceneNumberingDown() bool {
	return p.cache.Snapshot().ProviderEnabled(settings.SourceTheXEM) && p.outbound().breaker.State(providerTheXEM).State != httpx.BreakerClosed
}

// metadataProviderStatus is how a metadata provider stands right now, for Settings -> Metadata.
func (p *process) metadataProviderStatus(key string) apiserver.MetadataProviderStatus {
	shared, bucket, now := p.outbound(), bucketOf(key), clock.NewSystem().Now()
	if circuit := shared.breaker.State(bucket); circuit.State != httpx.BreakerClosed {
		if circuit.KeyRejected {
			return apiserver.MetadataProviderStatus{State: apiserver.ProviderKeyRejected, Until: circuit.RetryAt}
		}
		return apiserver.MetadataProviderStatus{State: apiserver.ProviderFailing, Until: circuit.RetryAt}
	}
	if until := shared.limiter.State(bucket).PausedUntil; until.After(now) {
		return apiserver.MetadataProviderStatus{State: apiserver.ProviderPaused, Until: until}
	}
	return apiserver.MetadataProviderStatus{State: apiserver.ProviderAnswering}
}
