package settings

import (
	"context"
	"fmt"
	"slices"

	"github.com/makearr/Solarr/internal/db/store/gen/settingsdb"
	"github.com/makearr/Solarr/internal/platform/config"
)

// The movie providers.
const (
	MovieSourceTMDB   = "tmdb"
	MovieSourceRadarr = "radarr"
)

// KeyMetadataMovieSources orders the movie providers: the first to hold a field supplies it, the others fill what is missing.
const KeyMetadataMovieSources = "metadata.movie_sources"

// DefaultMovieSources is TMDB, then Radarr's keyless service, which answers where there is no key.
var DefaultMovieSources = []string{MovieSourceTMDB, MovieSourceRadarr}

// MetadataProvider is one source of metadata: where it is useful and what it carries. The list is in
// documentation/metadata-providers.md, with the sources not built yet.
type MetadataProvider struct {
	Key, Label string
	MediaTypes []string
	// Carries is what it supplies, in the words a person reads.
	Carries []string
	// NeedsKey says the provider wants a key of the owner's own before it answers.
	NeedsKey bool
}

// MetadataProviders are the providers a library's order is made of.
var MetadataProviders = []MetadataProvider{
	{Key: SourceBrainzMash, Label: "BrainzMash", MediaTypes: []string{"music"}, Carries: []string{"artists", "albums", "tracklists", "credits", "genres", "links"}},
	{Key: SourceLidarrProxy, Label: "Lidarr's metadata service", MediaTypes: []string{"music"}, Carries: []string{"artists", "albums", "tracklists", "genres", "images", "links"}},
	{Key: SourceMusicBrainz, Label: "MusicBrainz", MediaTypes: []string{"music"}, Carries: []string{"artists", "albums", "tracklists", "credits", "genres", "links"}},
	{Key: TVSourceSkyhook, Label: "Sonarr's SkyHook", MediaTypes: []string{"tv"}, Carries: []string{"series", "seasons", "episodes", "air dates", "images", "ids"}},
	{Key: TVSourceTMDB, Label: "TMDB", MediaTypes: []string{"tv", "movie"}, Carries: []string{"overview", "cast and crew", "guest stars", "images", "certification", "networks and studios", "collections", "ids"}, NeedsKey: true},
	{Key: TVSourceTVDB, Label: "TheTVDB", MediaTypes: []string{"tv"}, Carries: []string{"series", "episodes", "other orderings", "images", "cast", "ids"}, NeedsKey: true},
	{Key: TVSourceTVmaze, Label: "TVmaze", MediaTypes: []string{"tv"}, Carries: []string{"air times and time zones", "networks", "cast", "images"}},
	{Key: TVSourceAniList, Label: "AniList", MediaTypes: []string{"tv"}, Carries: []string{"anime titles in every language", "cours", "relations", "genres", "images"}},
	{Key: MovieSourceRadarr, Label: "Radarr's metadata service", MediaTypes: []string{"movie"}, Carries: []string{"overview", "release dates", "certification", "ratings", "collections", "ids"}},
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

// DefaultMetadataSources is the order a library of the type, and kind within it, starts with: what the instance uses
// for the type, with anime first among the TV providers for an anime library, which AniList alone names properly.
func DefaultMetadataSources(mediaType, kind string) []string {
	switch mediaType {
	case "tv":
		if kind == "anime" {
			return []string{TVSourceAniList, TVSourceSkyhook, TVSourceTMDB, TVSourceTVDB, TVSourceTVmaze}
		}
		return []string{TVSourceSkyhook, TVSourceTMDB, TVSourceTVDB, TVSourceTVmaze}
	case "movie":
		return slices.Clone(DefaultMovieSources)
	case "music":
		return slices.Clone(DefaultMetadataSourceOrder)
	}
	return nil
}

// MetadataSourcesFor is the order a library's providers are consulted in: its own when it has set one, else the
// instance's for its media type.
func (s *Snapshot) MetadataSourcesFor(category Category) []string {
	if len(category.MetadataSources) > 0 {
		return category.MetadataSources
	}
	switch category.MediaType {
	case "tv":
		return s.policyStringListOr(KeyMetadataTVSources, DefaultTVSources)
	case "movie":
		return s.policyStringListOr(KeyMetadataMovieSources, DefaultMovieSources)
	case "music":
		if order := s.policyStringList(KeyMetadataSourceOrder); len(order) > 0 {
			return order
		}
		return DefaultMetadataSourceOrder
	}
	return nil
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
