package app

import (
	"time"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// Provider names of the clients built in this package; each is its own rate bucket.
const (
	providerWikidata       = "wikidata"
	providerWikipedia      = "wikipedia"
	providerWikimediaImage = "wikimedia-image"
	providerTheAudioDB     = "theaudiodb"
	providerArr            = "arr"
	providerProwlarr       = "prowlarr"
	providerPublicIP       = "public-ip"
	providerImages         = "images"
	providerSkyhook        = "skyhook"
	providerTVmaze         = "tvmaze"
	providerTMDB           = "tmdb"
	providerTVDB           = "tvdb"
	providerAniList        = "anilist"
	providerRadarrMeta     = "radarr-meta"
	providerMDBList        = "mdblist"
	providerOMDb           = "omdb"
	providerFanart         = "fanart"
	providerOpenLibrary    = "openlibrary"
	providerGoogleBooks    = "googlebooks"
	providerHardcover      = "hardcover"
	providerITunes         = "itunes"
	providerFantLab        = "fantlab"
	providerWhisparr       = "whisparr"
	providerStashDB        = "stashdb"
	providerThePornDB      = "theporndb"
	providerFansDB         = "fansdb"
	// providerAudible is Audible's catalog and Audnexus together: Audnexus publishes the pace both are asked at.
	providerAudible = "audible"
	// Each indexer and each download client connection has a bucket of its own, named by its id.
	providerIndexerPrefix    = "indexer:"
	providerDownloaderPrefix = "downloader:"
)

// providerLimits is every provider's pace, declared once (ADR-13): the metadata providers' in settings.MetadataProviders,
// where a person may set their own, the rest here. A provider not listed is not paced.
var providerLimits = withMetadataPaces(map[string]httpx.Limits{
	// Wikipedia's REST API allows 200/s.
	providerWikipedia: {MinInterval: 100 * time.Millisecond},
	// Someone's own *Arr server, often busy with its own imports.
	providerArr: {MinInterval: 500 * time.Millisecond},
	// A wall of posters must not starve the metadata calls.
	providerImages: {MinInterval: 100 * time.Millisecond},
})

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
			limits[bucketOf(provider.Key)] = httpx.Limits{MinInterval: paceInterval(provider.PerMinute)}
		}
	}
	return limits
}

// paceInterval is the gap between requests of a pace in requests a minute.
func paceInterval(perMinute int64) time.Duration { return time.Minute / time.Duration(perMinute) }

// livePace answers the pace a person set for the metadata provider behind a bucket (user, 2026-10-23).
func (p *process) livePace(bucket string) (time.Duration, bool) {
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
		return 0, false
	}
	return paceInterval(perMinute), true
}

// providerFamilyLimits pace each connection of a kind on its own bucket: a search fans out to every indexer at
// once (ADR-37), a download client is polled.
var providerFamilyLimits = map[string]httpx.Limits{
	providerIndexerPrefix:    {MinInterval: 500 * time.Millisecond},
	providerDownloaderPrefix: {MinInterval: 200 * time.Millisecond},
}

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
			limiter: httpx.NewLimiter(wall, httpx.Config{Providers: providerLimits, Prefixes: providerFamilyLimits, LivePace: p.livePace}, nil),
			breaker: httpx.NewBreaker(wall, httpx.BreakerConfig{}),
		}
	})
	return p.outboundShared
}

// providerClient builds a client on the shared budget. The provider name is its bucket; a nil guard is the default
// SSRF guard, and the global egress proxy and the user agent apply unless config sets its own.
func (p *process) providerClient(provider string, guard *httpx.Guard, config httpx.ClientConfig) *httpx.Client {
	shared := p.outbound()
	if guard == nil {
		guard = httpx.NewGuard(nil, nil, nil)
	}
	config.Provider = provider
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
