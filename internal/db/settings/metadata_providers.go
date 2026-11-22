package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/db/store/gen/settingsdb"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
)

// The movie providers.
const (
	MovieSourceTMDB    = "tmdb"
	MovieSourceRadarr  = "radarr"
	MovieSourceMDBList = "mdblist"
	MovieSourceOMDb    = "omdb"
	MovieSourceIMDb    = "imdb"
	MovieSourceFanart  = "fanart"
)

// The book and audiobook providers.
const (
	BookSourceOpenLibrary = "openlibrary"
	BookSourceGoogleBooks = "googlebooks"
	BookSourceAudible     = "audible"
	BookSourceITunes      = "itunes"
	BookSourceFantLab     = "fantlab"
	BookSourceHardcover   = "hardcover"
	BookSourceArchive     = "internetarchive"
)

// DefaultBookSources is Wikidata to steer, Open Library, which names a book, then Google Books to fill what it lacks.
var DefaultBookSources = []string{SourceWikidata, BookSourceOpenLibrary, BookSourceGoogleBooks, BookSourceHardcover, BookSourceArchive}

// DefaultAudiobookSources is Wikidata to steer, Audible for the reading (narrators, length, series), then the book providers.
var DefaultAudiobookSources = []string{SourceWikidata, BookSourceAudible, BookSourceITunes, BookSourceOpenLibrary, BookSourceGoogleBooks, BookSourceHardcover}

// The comic and manga providers.
const (
	ComicSourceComicVine    = "comicvine"
	ComicSourceMetron       = "metron"
	ComicSourceGCD          = "gcd"
	ComicSourceMangaUpdates = "mangaupdates"
	ComicSourceMangaDex     = "mangadex"
)

// DefaultComicSources is ComicVine, which names a series and numbers its issues, Metron, then the Grand Comics Database.
var DefaultComicSources = []string{ComicSourceComicVine, ComicSourceMetron, ComicSourceGCD}

// DefaultMangaSources is AniList, which names a manga, then MangaUpdates and MangaDex, whose chapter map gives the volumes.
var DefaultMangaSources = []string{TVSourceAniList, ComicSourceMangaUpdates, ComicSourceMangaDex}

// The adult providers: Whisparr's server, backed by ThePornDB, and three stash-box servers.
const (
	AdultSourceWhisparr  = "whisparr"
	AdultSourceStashDB   = "stashdb"
	AdultSourceThePornDB = "theporndb"
	AdultSourceFansDB    = "fansdb"
)

// DefaultAdultSources is Wikidata to steer, Whisparr's keyless server, then the stash-box servers StashDB, ThePornDB and FansDB.
var DefaultAdultSources = []string{SourceWikidata, AdultSourceWhisparr, AdultSourceStashDB, AdultSourceThePornDB, AdultSourceFansDB}

// SourceWikidata is Wikidata in a library's order: the ids its item records steer the other providers to the right entry.
const SourceWikidata = "wikidata"

// The providers that are no part of a library's order, only switched on or off.
const (
	SourceTheAudioDB      = "theaudiodb"
	SourceCoverArtArchive = "coverartarchive"
	SourceAcoustID        = "acoustid"
	SourceListenBrainz    = "listenbrainz"
	SourceDeezer          = "deezer"
	// SourceTheXEM maps TheTVDB's episode numbers to the ones scene releases carry.
	SourceTheXEM = "thexem"
)

// KeyMetadataDisabledProviders lists the providers switched off for every library.
const KeyMetadataDisabledProviders = "metadata.disabled_providers"

// DefaultDisabledProviders start off: Google Books answers 429 without a key of the owner's own, and the comic providers are new.
var DefaultDisabledProviders = []string{BookSourceGoogleBooks, MovieSourceIMDb, ComicSourceComicVine, ComicSourceMetron, ComicSourceGCD, ComicSourceMangaUpdates, ComicSourceMangaDex}

// KeyMetadataMovieSources orders the movie providers: the first to hold a field supplies it, the others fill what is missing.
const KeyMetadataMovieSources = "metadata.movie_sources"

// DefaultMovieSources is Wikidata to steer, TMDB, then Radarr's keyless service, which answers where there is no key.
var DefaultMovieSources = []string{SourceWikidata, MovieSourceTMDB, MovieSourceRadarr, MovieSourceIMDb, MovieSourceMDBList, MovieSourceOMDb, TVSourceTVDB, MovieSourceFanart}

// MetadataProvider is one source of metadata: where it is useful and what it carries. The list is in
// documentation/metadata-providers.md, with the sources not built yet.
type MetadataProvider struct {
	Key, Label string
	// MediaTypes are the libraries whose order the provider takes a place in.
	MediaTypes []string
	// AlsoFor are the libraries it serves outside their order: photos, covers, fingerprints.
	AlsoFor []string
	// Carries is what it supplies, in the words a person reads.
	Carries []string
	// NeedsKey says the provider wants a key of the owner's own before it answers.
	NeedsKey bool
	// PerMinute is the shipped pace, requests a minute; 0 is not paced. A person may set their own (ProviderPace).
	PerMinute int64
	// MaxPerMinute is the most the provider allows, where it publishes a limit; 0 is MaxProviderPerMinute.
	MaxPerMinute int64
	// Published is what the provider says it can take, in the words a person reads.
	Published string
	// Role says whether the provider takes a place in a library's order; the zero value is RoleOrder.
	Role ProviderRole
	// Provisional marks a local provider (a media server): asked first, ranked after every other provider in the merge.
	Provisional bool
}

// ProviderRole says how a provider is used: in a library's order, or switched on and off with a fixed place.
type ProviderRole string

// The roles of a provider.
const (
	RoleOrder  ProviderRole = "order"
	RoleEnrich ProviderRole = "enrich"
)

// InOrder says the provider takes a place in a library's order.
func (p MetadataProvider) InOrder() bool { return p.Role != RoleEnrich }

// LocalSourcePrefix starts the key of every local provider: "local:" and the media server kind.
const LocalSourcePrefix = "local:"

// LocalSource is the provider key of a media server kind.
func LocalSource(kind string) string { return LocalSourcePrefix + kind }

// IsProvisionalSource says key is a local provider, whose answers every other provider replaces.
func IsProvisionalSource(key string) bool { return strings.HasPrefix(key, LocalSourcePrefix) }

// RankProvisionalLast moves the provisional providers behind all the others, keeping each group's order: in a merge the
// first provider to hold a field supplies it, and a local value must lose to every external one.
func RankProvisionalLast(order []string) []string {
	ranked := make([]string, 0, len(order))
	var local []string
	for _, key := range order {
		if IsProvisionalSource(key) {
			local = append(local, key)
		} else {
			ranked = append(ranked, key)
		}
	}
	return append(ranked, local...)
}

// MaxProviderPerMinute bounds a pace no provider limits: 100 a second.
const MaxProviderPerMinute = 6000

// KeyMetadataProviderPaces holds the paces a person set, "provider:requests-per-minute" each.
const KeyMetadataProviderPaces = "metadata.provider_paces"

// MetadataProviders are the providers a library's order is made of.
var MetadataProviders = []MetadataProvider{
	{Key: SourceWikidata, Label: "Wikidata", MediaTypes: []string{"tv", "movie", "book", "audiobook", "adult"}, AlsoFor: []string{"music"}, Carries: []string{"the ids every other provider knows a work by", "franchises and spin-offs", "artist photos and biographies", "people across libraries"},
		PerMinute: 60, Published: "one request at a time; no rate published"},
	{Key: SourceBrainzMash, Label: "BrainzMash", MediaTypes: []string{"music"}, Carries: []string{"artists", "albums", "tracklists", "credits", "genres", "links"},
		PerMinute: 600, Published: "none published (its DroppedNeedle pool documents 10 a second)"},
	{Key: SourceLidarrProxy, Label: "Lidarr's metadata service", MediaTypes: []string{"music"}, Carries: []string{"artists", "albums", "tracklists", "genres", "images", "links"},
		PerMinute: 240, Published: "none published; shared by every Lidarr"},
	{Key: SourceMusicBrainz, Label: "MusicBrainz", MediaTypes: []string{"music"}, Carries: []string{"artists", "albums", "tracklists", "credits", "genres", "links"},
		PerMinute: 60, MaxPerMinute: 60, Published: "1 a second; faster gets the address blocked"},
	{Key: TVSourceSkyhook, Label: "Sonarr's SkyHook", MediaTypes: []string{"tv"}, Carries: []string{"series", "seasons", "episodes", "air dates", "images", "ids"},
		PerMinute: 600, Published: "none published; shared by every Sonarr"},
	{Key: TVSourceTMDB, Label: "TMDB", MediaTypes: []string{"tv", "movie"}, Carries: []string{"overview", "cast and crew", "guest stars", "images", "certification", "networks and studios", "collections", "ids"}, NeedsKey: true,
		PerMinute: 2400, Published: "no fixed limit; it throttles at around 40 a second"},
	{Key: TVSourceTVDB, Label: "TheTVDB", MediaTypes: []string{"tv", "movie"}, Carries: []string{"series", "episodes", "other orderings", "images", "cast", "ids"},
		PerMinute: 1200, Published: "none published"},
	{Key: TVSourceTVmaze, Label: "TVmaze", MediaTypes: []string{"tv"}, Carries: []string{"air times and time zones", "networks", "cast", "images"},
		PerMinute: 120, MaxPerMinute: 120, Published: "20 every 10 seconds"},
	{Key: TVSourceAniList, Label: "AniList", MediaTypes: []string{"tv", "comic"}, Carries: []string{"anime titles in every language", "cours", "relations", "genres", "images"},
		PerMinute: 30, MaxPerMinute: 90, Published: "90 a minute, 30 while it runs degraded"},
	{Key: TVSourceKitsu, Label: "Kitsu", MediaTypes: []string{"tv"}, Carries: []string{"anime synopses", "age ratings", "average ratings", "posters", "ids on AniList, MyAnimeList and AniDB"},
		PerMinute: 60, Published: "none published; asked at 60 a minute"},
	{Key: MovieSourceRadarr, Label: "Radarr's metadata service", MediaTypes: []string{"movie"}, Carries: []string{"overview", "release dates", "certification", "ratings", "collections", "ids"},
		PerMinute: 240, Published: "none published; shared by every Radarr"},
	{Key: MovieSourceMDBList, Label: "MDBList", MediaTypes: []string{"movie"}, Carries: []string{"ratings of IMDb, TMDB, Trakt, Letterboxd, Rotten Tomatoes and Metacritic"}, NeedsKey: true,
		PerMinute: 200, MaxPerMinute: 200, Published: "1,000 a day on a free key"},
	{Key: MovieSourceIMDb, Label: "IMDb ratings", MediaTypes: []string{"movie", "tv"}, Carries: []string{"IMDb rating and votes, from IMDb's daily download (personal, non-commercial use)"},
		Published: "one download a day; no key, no quota"},
	{Key: MovieSourceOMDb, Label: "OMDb", MediaTypes: []string{"movie", "tv"}, Carries: []string{"IMDb, Rotten Tomatoes and Metacritic ratings", "English plots"}, NeedsKey: true,
		PerMinute: 60, Published: "1,000 a day on a free key"},
	{Key: MovieSourceFanart, Label: "fanart.tv", MediaTypes: []string{"movie", "tv"}, AlsoFor: []string{"music"}, Carries: []string{"clear logos", "clear art", "disc art", "banners", "backgrounds", "artist backgrounds and logos", "album covers"}, NeedsKey: true,
		PerMinute: 240, Published: "none published"},
	{Key: BookSourceOpenLibrary, Label: "Open Library", MediaTypes: []string{"book", "audiobook"}, Carries: []string{"works and editions", "authors", "ISBNs", "subjects", "covers", "first publication"},
		PerMinute: 60, Published: "none published; it asks clients to identify themselves and stay gentle"},
	{Key: BookSourceGoogleBooks, Label: "Google Books", MediaTypes: []string{"book", "audiobook"}, Carries: []string{"descriptions", "publishers", "page counts", "categories", "ratings", "covers"},
		PerMinute: 60, Published: "1,000 a day without a key; your own free key raises it"},
	{Key: BookSourceAudible, Label: "Audible (through Audnexus)", MediaTypes: []string{"audiobook"}, Carries: []string{"narrators", "length", "series and position", "publishers", "genres", "covers"},
		PerMinute: 100, MaxPerMinute: 100, Published: "Audnexus: 100 a minute"},
	{Key: BookSourceHardcover, Label: "Hardcover", MediaTypes: []string{"book", "audiobook"}, Carries: []string{"descriptions", "ratings", "genres", "page counts", "covers"}, NeedsKey: true,
		PerMinute: 30, MaxPerMinute: 60, Published: "60 a minute"},
	{Key: BookSourceArchive, Label: "Internet Archive", MediaTypes: []string{"book"}, Carries: []string{"descriptions", "subjects", "language", "covers"},
		PerMinute: 30, Published: "none published; asked at 30 a minute"},
	{Key: BookSourceITunes, Label: "iTunes", MediaTypes: []string{"audiobook"}, Carries: []string{"audiobook titles and authors", "descriptions", "genres", "large covers"},
		PerMinute: 20, MaxPerMinute: 20, Published: "about 20 a minute"},
	{Key: BookSourceFantLab, Label: "FantLab", MediaTypes: []string{"book", "audiobook"}, Carries: []string{"fantasy and science fiction works", "original titles", "descriptions", "ratings", "covers"},
		PerMinute: 60, Published: "none published"},
	{Key: ComicSourceComicVine, Label: "ComicVine", MediaTypes: []string{"comic"}, Carries: []string{"series", "issues and their numbers", "cover and store dates", "credits", "story arcs", "covers"}, NeedsKey: true,
		PerMinute: 3, MaxPerMinute: 3, Published: "200 requests an hour for each kind of request; faster gets the key blocked"},
	{Key: ComicSourceMetron, Label: "Metron (account as user:password)", MediaTypes: []string{"comic"}, Carries: []string{"series and their type", "issues", "page counts", "age ratings", "credits", "reprints", "ComicVine and GCD ids"}, NeedsKey: true,
		PerMinute: 20, MaxPerMinute: 30, Published: "about 30 a minute and a daily cap"},
	{Key: ComicSourceGCD, Label: "Grand Comics Database (comics.org, CC BY-SA 4.0)", MediaTypes: []string{"comic"}, Carries: []string{"series", "issues", "publishers", "bibliographic detail"},
		PerMinute: 30, Published: "none published; asked at 30 a minute"},
	{Key: ComicSourceMangaUpdates, Label: "MangaUpdates", MediaTypes: []string{"comic"}, Carries: []string{"titles and associated names", "type", "status and volumes", "publishers", "genres", "authors"},
		PerMinute: 30, Published: "none published; asked at 30 a minute"},
	{Key: ComicSourceMangaDex, Label: "MangaDex", MediaTypes: []string{"comic"}, Carries: []string{"titles", "content rating", "volumes and their chapters", "covers", "ids on AniList and MangaUpdates"},
		PerMinute: 240, MaxPerMinute: 300, Published: "about 5 a second; faster gets the address banned"},
	{Key: AdultSourceWhisparr, Label: "Whisparr's metadata server", MediaTypes: []string{"adult"}, Carries: []string{"scenes", "performer credits", "sites and studios", "genres", "images"},
		PerMinute: 240, Published: "none published; shared by every Whisparr"},
	{Key: AdultSourceStashDB, Label: "StashDB", MediaTypes: []string{"adult"}, Carries: []string{"scenes", "performers", "studios and networks", "tags", "fingerprints", "images"}, NeedsKey: true,
		PerMinute: 120, Published: "none published; a community server, asked gently"},
	{Key: AdultSourceThePornDB, Label: "ThePornDB", MediaTypes: []string{"adult"}, Carries: []string{"scenes", "performers", "sites", "movies", "tags", "fingerprints", "images"}, NeedsKey: true,
		PerMinute: 120, Published: "none published"},
	{Key: AdultSourceFansDB, Label: "FansDB", MediaTypes: []string{"adult"}, Carries: []string{"fan-platform scenes", "creators", "tags", "fingerprints", "images"}, NeedsKey: true,
		PerMinute: 120, Published: "none published; a community server, asked gently"},
	{Key: LocalSource("jellyfin"), Label: "Jellyfin (your server)", MediaTypes: []string{"tv", "movie", "music"}, Provisional: true, Carries: []string{"titles", "overviews", "genres", "cast", "provider ids", "images"},
		PerMinute: 600, Published: "your own server"},
	{Key: LocalSource("navidrome"), Label: "Navidrome (your server)", MediaTypes: []string{"music"}, Provisional: true, Carries: []string{"artists", "albums", "tracks", "MusicBrainz ids"},
		PerMinute: 600, Published: "your own server"},
	{Key: LocalSource("audiobookshelf"), Label: "Audiobookshelf (your server)", MediaTypes: []string{"audiobook", "book"}, Provisional: true, Carries: []string{"titles", "narrators", "series", "ASIN and ISBN", "covers"},
		PerMinute: 600, Published: "your own server"},
	{Key: LocalSource("calibre"), Label: "Calibre (your server)", MediaTypes: []string{"book"}, Provisional: true, Carries: []string{"titles", "authors", "series", "tags", "identifiers", "covers"},
		PerMinute: 600, Published: "your own server"},
	{Key: LocalSource("stash"), Label: "Stash (your server)", MediaTypes: []string{"adult"}, Provisional: true, Carries: []string{"scenes", "performers", "studios", "tags", "stash-box ids"},
		PerMinute: 600, Published: "your own server"},
	{Key: SourceTheAudioDB, Role: RoleEnrich, Label: "TheAudioDB", AlsoFor: []string{"music"}, Carries: []string{"artist photos and biographies", "album covers"},
		PerMinute: 120, Published: "none published for the free key"},
	{Key: SourceCoverArtArchive, Role: RoleEnrich, Label: "Cover Art Archive", AlsoFor: []string{"music"}, Carries: []string{"album covers"},
		Published: "none published; served by the Internet Archive"},
	{Key: SourceListenBrainz, Role: RoleEnrich, Label: "ListenBrainz", AlsoFor: []string{"music"}, Carries: []string{"how often an album was listened to"},
		PerMinute: 60, MaxPerMinute: 60, Published: "none published; asked at 60 a minute"},
	{Key: SourceDeezer, Role: RoleEnrich, Label: "Deezer", AlsoFor: []string{"music"}, Carries: []string{"an album's fans", "explicit flag"},
		PerMinute: 120, Published: "50 requests every 5 seconds"},
	{Key: SourceAcoustID, Role: RoleEnrich, Label: "AcoustID", AlsoFor: []string{"music"}, Carries: []string{"files identified by their sound"}, NeedsKey: true,
		PerMinute: 180, MaxPerMinute: 180, Published: "3 a second"},
	{Key: SourceTheXEM, Role: RoleEnrich, Label: "TheXEM", AlsoFor: []string{"tv"}, Carries: []string{"scene numbering", "scene names"},
		PerMinute: 30, Published: "none published; a community server, asked gently"},
}

// musicSources are the music providers of which at least one must stay on: every music lookup asks one of them.
var musicSources = []string{SourceBrainzMash, SourceLidarrProxy, SourceMusicBrainz}

// ProviderPace is a provider's pace in requests a minute, a person's own where set, else the shipped one; 0 is not paced.
func (s *Snapshot) ProviderPace(key string) (perMinute int64, own bool) {
	for _, entry := range s.policyStringList(KeyMetadataProviderPaces) {
		name, value, found := strings.Cut(entry, ":")
		if !found || name != key {
			continue
		}
		if parsed, err := strconv.ParseInt(value, 10, 64); err == nil && parsed > 0 {
			return parsed, true
		}
	}
	provider, _ := MetadataProviderByKey(key)
	return provider.PerMinute, false
}

// SetProviderPace sets a provider's pace in requests a minute, up to what it allows; 0 returns it to the shipped pace.
func (c *Cache) SetProviderPace(ctx context.Context, cl clock.Clock, key string, perMinute int64) error {
	provider, known := MetadataProviderByKey(key)
	if !known {
		return config.Problems{{Path: "provider", Value: key, Expectation: "a metadata provider"}}
	}
	ceiling := provider.MaxPerMinute
	if ceiling == 0 {
		ceiling = MaxProviderPerMinute
	}
	if perMinute < 0 || perMinute > ceiling {
		return config.Problems{{Path: "per_minute", Value: strconv.FormatInt(perMinute, 10),
			Expectation: fmt.Sprintf("0 (the shipped pace) to %d, the most %s allows", ceiling, provider.Label)}}
	}
	entries := slices.DeleteFunc(slices.Clone(c.Snapshot().policyStringList(KeyMetadataProviderPaces)), func(entry string) bool {
		return strings.HasPrefix(entry, key+":")
	})
	if perMinute > 0 {
		entries = append(entries, key+":"+strconv.FormatInt(perMinute, 10))
	}
	encoded, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	return c.setPolicy(ctx, cl, KeyMetadataProviderPaces, string(encoded))
}

// MetadataProviderByKey finds a provider by its key.
func MetadataProviderByKey(key string) (MetadataProvider, bool) {
	index := slices.IndexFunc(MetadataProviders, func(provider MetadataProvider) bool { return provider.Key == key })
	if index < 0 {
		return MetadataProvider{}, false
	}
	return MetadataProviders[index], true
}

// switchedBy are the providers whose on and off is their own older setting, which their other uses read too.
var switchedBy = map[string]string{SourceWikidata: KeyMetadataWikidata, SourceTheAudioDB: KeyMetadataTheAudioDB, SourceAcoustID: KeyMetadataAcoustID}

// ProviderEnabled says a provider is switched on for every library.
func (s *Snapshot) ProviderEnabled(key string) bool {
	if setting, own := switchedBy[key]; own {
		return s.policyBool(setting, true)
	}
	return !slices.Contains(s.policyStringListOr(KeyMetadataDisabledProviders, DefaultDisabledProviders), key)
}

// SetProviderEnabled switches a provider on or off for every library; the last music source on stays on, since every music
// lookup needs one (direct MusicBrainz too may go, user 2026-10-23).
func (c *Cache) SetProviderEnabled(ctx context.Context, cl clock.Clock, key string, enabled bool) error {
	if _, known := MetadataProviderByKey(key); !known {
		return config.Problems{{Path: "provider", Value: key, Expectation: "a metadata provider"}}
	}
	if !enabled && slices.Contains(musicSources, key) && !c.Snapshot().anotherMusicSourceOn(key) {
		return config.Problems{{Path: "provider", Value: key,
			Expectation: "one of BrainzMash, Lidarr's metadata service and MusicBrainz switched on: music is looked up in one of them"}}
	}
	if setting, own := switchedBy[key]; own {
		return c.setPolicy(ctx, cl, setting, strconv.FormatBool(enabled))
	}
	disabled := slices.DeleteFunc(slices.Clone(c.Snapshot().policyStringListOr(KeyMetadataDisabledProviders, DefaultDisabledProviders)), func(one string) bool { return one == key })
	if !enabled {
		disabled = append(disabled, key)
	}
	encoded, err := json.Marshal(disabled)
	if err != nil {
		return err
	}
	return c.setPolicy(ctx, cl, KeyMetadataDisabledProviders, string(encoded))
}

// ProviderCanBeSwitchedOff says switching key off leaves a music source on; every provider but the last of those can go.
func (s *Snapshot) ProviderCanBeSwitchedOff(key string) bool {
	return !slices.Contains(musicSources, key) || !s.ProviderEnabled(key) || s.anotherMusicSourceOn(key)
}

// anotherMusicSourceOn says a music source other than key is switched on.
func (s *Snapshot) anotherMusicSourceOn(key string) bool {
	return slices.ContainsFunc(musicSources, func(source string) bool { return source != key && s.ProviderEnabled(source) })
}

// MetadataProvidersFor lists the providers that apply to a media type.
func MetadataProvidersFor(mediaType string) []MetadataProvider {
	var out []MetadataProvider
	for _, provider := range MetadataProviders {
		if slices.Contains(provider.MediaTypes, mediaType) {
			out = append(out, provider)
		}
	}
	return out
}

// DefaultMetadataSources is the order a library of the type, and kind within it, starts with: Wikidata first to steer the
// others, and AniList first among the TV providers for an anime library, which AniList alone names properly.
func DefaultMetadataSources(mediaType, kind string) []string {
	switch mediaType {
	case "tv":
		if kind == "anime" {
			return []string{SourceWikidata, TVSourceAniList, TVSourceSkyhook, TVSourceTMDB, TVSourceTVDB, TVSourceTVmaze, TVSourceKitsu, MovieSourceIMDb, TVSourceOMDb, TVSourceFanart}
		}
		return []string{SourceWikidata, TVSourceSkyhook, TVSourceTMDB, TVSourceTVDB, TVSourceTVmaze, MovieSourceIMDb, TVSourceOMDb, TVSourceFanart}
	case "movie":
		return slices.Clone(DefaultMovieSources)
	case "book":
		return slices.Clone(DefaultBookSources)
	case "audiobook":
		return slices.Clone(DefaultAudiobookSources)
	case "adult":
		return slices.Clone(DefaultAdultSources)
	case "comic":
		if kind == "manga" || kind == "hentai" {
			return slices.Clone(DefaultMangaSources)
		}
		return slices.Clone(DefaultComicSources)
	case "music":
		return slices.Clone(DefaultMetadataSourceOrder)
	}
	return nil
}

// MetadataSourcesFor is the order a library's providers are consulted in: its own when it has set one, else its type's
// default (shipped: what suits its type and kind); a provider switched off is left out.
func (s *Snapshot) MetadataSourcesFor(category Category) []string {
	order := slices.Clone(category.MetadataSources)
	local := s.localSources[category.ID]
	if len(order) == 0 {
		order = append(slices.Clone(local), s.TypeMetadataSources(category.MediaType, category.Kind)...)
	}
	order = slices.DeleteFunc(order, func(key string) bool { return IsProvisionalSource(key) && !slices.Contains(local, key) })
	return RankProvisionalLast(slices.DeleteFunc(slices.Clone(order), func(key string) bool { return !s.ProviderEnabled(key) }))
}

// LocalSourceOffered says a library is mapped to a media server of the local provider's kind that serves as metadata.
func (s *Snapshot) LocalSourceOffered(categoryID int64, key string) bool {
	return slices.Contains(s.localSources[categoryID], key)
}

// LocalSourceInUse says some library is mapped to such a media server; a local provider no one uses is not listed.
func (s *Snapshot) LocalSourceInUse(key string) bool {
	for _, keys := range s.localSources {
		if slices.Contains(keys, key) {
			return true
		}
	}
	return false
}

// LocalSourcesOf lists the local providers a library is mapped to.
func (s *Snapshot) LocalSourcesOf(categoryID int64) []string {
	return slices.Clone(s.localSources[categoryID])
}

// MusicSourceOrder is the order a music library's lookups are asked in; a lookup made for no music library (categoryID 0,
// or another type's) takes the first music library's.
func (s *Snapshot) MusicSourceOrder(categoryID int64) []string {
	library, found := s.Category(categoryID)
	if !found || library.MediaType != "music" {
		library = Category{MediaType: "music"}
		if index := slices.IndexFunc(s.Categories(), func(category Category) bool { return category.MediaType == "music" }); index >= 0 {
			library = s.Categories()[index]
		}
	}
	order := s.MetadataSourcesFor(library)
	// Direct MusicBrainz, while switched on, answers after the library's own order (ADR-29).
	if s.ProviderEnabled(SourceMusicBrainz) && !slices.Contains(order, SourceMusicBrainz) {
		order = append(order, SourceMusicBrainz)
	}
	return order
}

// IncludesBootlegOnly says the library lists release groups that only exist as bootlegs; no library (0) leaves them out.
func (s *Snapshot) IncludesBootlegOnly(categoryID int64) bool {
	library, found := s.Category(categoryID)
	return found && library.IncludeBootlegOnly
}

func validSourceFor(mediaType, source string) bool {
	return slices.ContainsFunc(MetadataProvidersFor(mediaType), func(provider MetadataProvider) bool { return provider.Key == source })
}

// SetCategoryMetadataSources replaces a library's provider order; an empty list returns it to the instance's.
func (c *Cache) SetCategoryMetadataSources(ctx context.Context, id int64, sources []string) error {
	category, found := c.Snapshot().Category(id)
	if !found {
		return fmt.Errorf("settings: no category %d", id)
	}
	var problems config.Problems
	seen := map[string]bool{}
	for index, source := range sources {
		if !validSourceFor(category.MediaType, source) || seen[source] || (IsProvisionalSource(source) && !c.Snapshot().LocalSourceOffered(id, source)) {
			problems = append(problems, config.Problem{Path: fmt.Sprintf("sources[%d]", index), Value: source, Expectation: "a provider of " + category.MediaType + " libraries, once"})
		}
		seen[source] = true
	}
	if len(problems) > 0 {
		return problems
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		if err := queries.DeleteCategoryMetadataSources(ctx, id); err != nil {
			return err
		}
		for index, source := range sources {
			if err := queries.InsertCategoryMetadataSource(ctx, settingsdb.InsertCategoryMetadataSourceParams{CategoryID: id, Source: source, Ord: int64(index)}); err != nil {
				return err
			}
		}
		return nil
	})
}

// SetCategoryIncludeBootlegOnly switches a library's listing of bootleg-only release groups on or off.
func (c *Cache) SetCategoryIncludeBootlegOnly(ctx context.Context, id int64, include bool) error {
	if _, found := c.Snapshot().Category(id); !found {
		return fmt.Errorf("%w: category %d", ErrNotFound, id)
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		if include {
			return queries.SetCategoryBootlegOnly(ctx, id)
		}
		return queries.ClearCategoryBootlegOnly(ctx, id)
	})
}
