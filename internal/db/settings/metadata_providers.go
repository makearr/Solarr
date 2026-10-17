package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"

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
	MovieSourceFanart  = "fanart"
)

// SourceWikidata is Wikidata in a library's order: the ids its item records steer the other providers to the right entry.
const SourceWikidata = "wikidata"

// The providers that are no part of a library's order, only switched on or off.
const (
	SourceTheAudioDB      = "theaudiodb"
	SourceCoverArtArchive = "coverartarchive"
	SourceAcoustID        = "acoustid"
)

// KeyMetadataDisabledProviders lists the providers switched off for every library.
const KeyMetadataDisabledProviders = "metadata.disabled_providers"

// KeyMetadataMovieSources orders the movie providers: the first to hold a field supplies it, the others fill what is missing.
const KeyMetadataMovieSources = "metadata.movie_sources"

// DefaultMovieSources is Wikidata to steer, TMDB, then Radarr's keyless service, which answers where there is no key.
var DefaultMovieSources = []string{SourceWikidata, MovieSourceTMDB, MovieSourceRadarr, MovieSourceMDBList, MovieSourceOMDb, TVSourceTVDB, MovieSourceFanart}

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
}

// MetadataProviders are the providers a library's order is made of.
var MetadataProviders = []MetadataProvider{
	{Key: SourceWikidata, Label: "Wikidata", MediaTypes: []string{"tv", "movie"}, AlsoFor: []string{"music"}, Carries: []string{"the ids every other provider knows a work by", "franchises and spin-offs", "artist photos and biographies", "people across libraries"}},
	{Key: SourceBrainzMash, Label: "BrainzMash", MediaTypes: []string{"music"}, Carries: []string{"artists", "albums", "tracklists", "credits", "genres", "links"}},
	{Key: SourceLidarrProxy, Label: "Lidarr's metadata service", MediaTypes: []string{"music"}, Carries: []string{"artists", "albums", "tracklists", "genres", "images", "links"}},
	{Key: SourceMusicBrainz, Label: "MusicBrainz", MediaTypes: []string{"music"}, Carries: []string{"artists", "albums", "tracklists", "credits", "genres", "links"}},
	{Key: TVSourceSkyhook, Label: "Sonarr's SkyHook", MediaTypes: []string{"tv"}, Carries: []string{"series", "seasons", "episodes", "air dates", "images", "ids"}},
	{Key: TVSourceTMDB, Label: "TMDB", MediaTypes: []string{"tv", "movie"}, Carries: []string{"overview", "cast and crew", "guest stars", "images", "certification", "networks and studios", "collections", "ids"}, NeedsKey: true},
	{Key: TVSourceTVDB, Label: "TheTVDB", MediaTypes: []string{"tv", "movie"}, Carries: []string{"series", "episodes", "other orderings", "images", "cast", "ids"}},
	{Key: TVSourceTVmaze, Label: "TVmaze", MediaTypes: []string{"tv"}, Carries: []string{"air times and time zones", "networks", "cast", "images"}},
	{Key: TVSourceAniList, Label: "AniList", MediaTypes: []string{"tv"}, Carries: []string{"anime titles in every language", "cours", "relations", "genres", "images"}},
	{Key: MovieSourceRadarr, Label: "Radarr's metadata service", MediaTypes: []string{"movie"}, Carries: []string{"overview", "release dates", "certification", "ratings", "collections", "ids"}},
	{Key: MovieSourceMDBList, Label: "MDBList", MediaTypes: []string{"movie"}, Carries: []string{"ratings of IMDb, TMDB, Trakt, Letterboxd, Rotten Tomatoes and Metacritic"}, NeedsKey: true},
	{Key: MovieSourceOMDb, Label: "OMDb", MediaTypes: []string{"movie"}, Carries: []string{"IMDb, Rotten Tomatoes and Metacritic ratings", "English plots"}, NeedsKey: true},
	{Key: MovieSourceFanart, Label: "fanart.tv", MediaTypes: []string{"movie"}, Carries: []string{"clear logos", "clear art", "disc art", "banners", "backgrounds"}, NeedsKey: true},
	{Key: SourceTheAudioDB, Label: "TheAudioDB", AlsoFor: []string{"music"}, Carries: []string{"artist photos and biographies", "album covers"}},
	{Key: SourceCoverArtArchive, Label: "Cover Art Archive", AlsoFor: []string{"music"}, Carries: []string{"album covers"}},
	{Key: SourceAcoustID, Label: "AcoustID", AlsoFor: []string{"music"}, Carries: []string{"files identified by their sound"}, NeedsKey: true},
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
	return !slices.Contains(s.policyStringList(KeyMetadataDisabledProviders), key)
}

// SetProviderEnabled switches a provider on or off for every library; MusicBrainz, which always answers, stays on.
func (c *Cache) SetProviderEnabled(ctx context.Context, cl clock.Clock, key string, enabled bool) error {
	if _, known := MetadataProviderByKey(key); !known || key == SourceMusicBrainz {
		return config.Problems{{Path: "provider", Value: key, Expectation: "a metadata provider that can be switched off"}}
	}
	if setting, own := switchedBy[key]; own {
		return c.setPolicy(ctx, cl, setting, strconv.FormatBool(enabled))
	}
	disabled := slices.DeleteFunc(slices.Clone(c.Snapshot().policyStringList(KeyMetadataDisabledProviders)), func(one string) bool { return one == key })
	if !enabled {
		disabled = append(disabled, key)
	}
	encoded, err := json.Marshal(disabled)
	if err != nil {
		return err
	}
	return c.setPolicy(ctx, cl, KeyMetadataDisabledProviders, string(encoded))
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
			return []string{SourceWikidata, TVSourceAniList, TVSourceSkyhook, TVSourceTMDB, TVSourceTVDB, TVSourceTVmaze}
		}
		return []string{SourceWikidata, TVSourceSkyhook, TVSourceTMDB, TVSourceTVDB, TVSourceTVmaze}
	case "movie":
		return slices.Clone(DefaultMovieSources)
	case "music":
		return slices.Clone(DefaultMetadataSourceOrder)
	}
	return nil
}

// MetadataSourcesFor is the order a library's providers are consulted in: its own when it has set one, else what suits
// its type and kind; a provider switched off is left out.
func (s *Snapshot) MetadataSourcesFor(category Category) []string {
	order := category.MetadataSources
	if len(order) == 0 {
		order = DefaultMetadataSources(category.MediaType, category.Kind)
	}
	return slices.DeleteFunc(slices.Clone(order), func(key string) bool { return !s.ProviderEnabled(key) })
}

// MusicSourceOrder is the order music lookups are asked in, the first music library's: lookups are made once for every
// library, so one order rules them.
func (s *Snapshot) MusicSourceOrder() []string {
	for _, category := range s.Categories() {
		if category.MediaType == "music" {
			return s.MetadataSourcesFor(category)
		}
	}
	return s.MetadataSourcesFor(Category{MediaType: "music"})
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
		if !validSourceFor(category.MediaType, source) || seen[source] {
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
