package adapters

import (
	"context"
	"fmt"
	"strconv"

	"github.com/makearr/MediaNexus/internal/api/apiserver"
	"github.com/makearr/MediaNexus/internal/db/library"
	"github.com/makearr/MediaNexus/internal/identify/priority"
	"github.com/makearr/MediaNexus/internal/media/music"
	"github.com/makearr/MediaNexus/internal/metadata/musicbrainz"
	"github.com/makearr/MediaNexus/internal/platform/clock"
)

// Discover satisfies apiserver.Discover over musicbrainz.Provider and
// library.Identity/Desired -- ADR-56's per-library half only (todo 4.17): a
// media type with no configured provider answers
// apiserver.ErrDiscoverUnsupportedMediaType, never a guess. Music is the only
// one wired today.
type Discover struct {
	Provider         musicbrainz.Provider
	Identity         library.Identity
	Desired          library.Desired
	Categories       Categories
	DefaultProfileID int64
	Clock            clock.Clock
}

// Search resolves categoryID to its media type and, for music, runs a
// free-text MusicBrainz release-group search (PriorityInteractive: a user is
// looking at this right now, ADR-29's own top priority).
func (a Discover) Search(ctx context.Context, categoryID int64, query string, limit int) ([]apiserver.DiscoverResult, error) {
	if len([]rune(query)) < 2 {
		return nil, apiserver.ErrQueryTooShort
	}
	category, ok := a.Categories.Category(categoryID)
	if !ok || category.MediaType != "music" {
		return nil, apiserver.ErrDiscoverUnsupportedMediaType
	}

	matches, err := a.Provider.SearchByQuery(ctx, musicbrainz.PriorityInteractive, query, limit)
	if err != nil {
		return nil, fmt.Errorf("musicbrainz: %w", err)
	}
	results := make([]apiserver.DiscoverResult, len(matches))
	for i, match := range matches {
		results[i] = apiserver.DiscoverResult{
			Provider: music.ProviderReleaseGroup, ExternalID: match.MBID,
			Title: match.Title, Artist: match.ArtistCredit, Year: releaseYear(match.FirstReleaseDate),
		}
	}
	return results, nil
}

// Add resolves-or-creates the work behind (provider, externalID) and ensures
// a monitored album-level desired_item for it -- library.Identity.ResolveWorks
// and library.Desired.EnsureAlbumItem, the exact primitives
// music.SubscriptionExpandJob's own ensureItemFor already uses for a work
// with no file and no edition yet. No edition is created here either, the
// same accepted gap subscription-discovered works already carry (no
// enrichment or artwork until something else gives one an edition).
func (a Discover) Add(
	ctx context.Context, categoryID int64, provider, externalID string, monitored bool,
) (apiserver.DiscoverAddResult, error) {
	category, ok := a.Categories.Category(categoryID)
	if !ok || category.MediaType != "music" || provider != music.ProviderReleaseGroup {
		return apiserver.DiscoverAddResult{}, apiserver.ErrDiscoverUnsupportedMediaType
	}

	// Re-fetched from the provider, never trusted from the request: an
	// external id names identity (ADR-8), the title that goes with it is the
	// provider's to say, not the caller's.
	releaseGroup, _, err := a.Provider.Album(ctx, musicbrainz.PriorityInteractive, externalID, "")
	if err != nil {
		return apiserver.DiscoverAddResult{}, fmt.Errorf("musicbrainz: %w", err)
	}

	now := a.Clock.Now()
	workIDs, err := a.Identity.ResolveWorks(ctx, now, []library.WorkLink{{
		Provider: music.ProviderReleaseGroup, ExternalID: externalID, Confidence: 1.0,
		Work: library.WorkIdentity{
			MediaType: "music", CanonicalTitle: releaseGroup.Title, SortTitle: music.SortTitle(releaseGroup.Title),
		},
	}})
	if err != nil {
		return apiserver.DiscoverAddResult{}, fmt.Errorf("resolve work: %w", err)
	}

	profileID := a.DefaultProfileID
	if category.QualityProfileID != nil {
		profileID = *category.QualityProfileID
	}
	item, err := a.Desired.EnsureAlbumItem(ctx, library.AlbumRequest{
		WorkID: workIDs[0], CategoryID: category.ID, ProfileID: profileID, Monitored: monitored,
	})
	if err != nil {
		return apiserver.DiscoverAddResult{}, fmt.Errorf("ensure album item: %w", err)
	}

	score := priority.Compute(priority.DefaultWeights(), priority.Input{
		Trigger: priority.TriggerUserRequest, CategoryRank: category.PriorityRank, QueuedSince: now,
	}, now)
	return apiserver.DiscoverAddResult{WorkID: workIDs[0], DesiredItemID: item.ID, Priority: score}, nil
}

// releaseYear reads only the leading year off a MusicBrainz partial date
// ("1978", "1978-01", "1978-01-12") -- Discover's result list shows a year
// for context, not the day precision todo 4.20's own FreshSince cares about.
func releaseYear(date string) *int64 {
	if len(date) < 4 {
		return nil
	}
	year, err := strconv.ParseInt(date[:4], 10, 64)
	if err != nil {
		return nil
	}
	return &year
}
