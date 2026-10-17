package app

import (
	"time"

	"github.com/makearr/Solarr/internal/metadata/acoustid"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
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
	// Each indexer and each download client connection has a bucket of its own, named by its id.
	providerIndexerPrefix    = "indexer:"
	providerDownloaderPrefix = "downloader:"
)

// providerLimits is every provider's pace, declared once (ADR-13). A provider not listed is not paced.
var providerLimits = map[string]httpx.Limits{
	// MusicBrainz allows one request per second and means it (ADR-29).
	musicbrainz.DirectName: {MinInterval: time.Second},
	// DroppedNeedle's documented 10 req/s for the pool; BrainzMash publishes no quota.
	musicbrainz.BrainzMashName: {MinInterval: 100 * time.Millisecond},
	// Lidarr's own service, shared infrastructure this project does not operate.
	musicbrainz.ProxyName: {MinInterval: 250 * time.Millisecond},
	// acoustid.org's published 3 requests per second.
	acoustid.Name: {MinInterval: 334 * time.Millisecond},
	// Wikidata's action API asks for serial requests; Wikipedia's REST API allows 200/s.
	providerWikidata:   {MinInterval: time.Second},
	providerWikipedia:  {MinInterval: 100 * time.Millisecond},
	providerTheAudioDB: {MinInterval: 500 * time.Millisecond},
	// Someone's own *Arr server, often busy with its own imports.
	providerArr:     {MinInterval: 500 * time.Millisecond},
	providerSkyhook: {MinInterval: 100 * time.Millisecond},
	// TVmaze publishes 20 calls per 10 seconds.
	providerTVmaze:  {MinInterval: 500 * time.Millisecond},
	providerTMDB:    {MinInterval: 25 * time.Millisecond},
	providerTVDB:    {MinInterval: 50 * time.Millisecond},
	providerAniList: {MinInterval: 2 * time.Second},
	// Radarr's own metadata service: shared infrastructure this project does not operate.
	providerRadarrMeta: {MinInterval: 250 * time.Millisecond},
	// MDBList's 1,000 reads per five minutes; OMDb's free key 1,000 a day, so it is never hurried; fanart.tv publishes none.
	providerMDBList: {MinInterval: 300 * time.Millisecond},
	providerOMDb:    {MinInterval: time.Second},
	providerFanart:  {MinInterval: 250 * time.Millisecond},
	// A wall of posters must not starve the metadata calls.
	providerImages: {MinInterval: 100 * time.Millisecond},
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
			limiter: httpx.NewLimiter(wall, httpx.Config{Providers: providerLimits, Prefixes: providerFamilyLimits}, nil),
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
