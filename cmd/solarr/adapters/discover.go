package adapters

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/priority"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
	"github.com/makearr/Solarr/internal/platform/clock"
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
	People           library.People
	Categories       Categories
	DefaultProfileID int64
	Clock            clock.Clock
	// Enrich starts fetching an added album's tracklist now rather than at the next hourly sweep.
	Enrich func(context.Context)
	// SearchAlbum searches one added album at once; ExpandArtist adds a new artist's albums now.
	SearchAlbum  func(ctx context.Context, categoryID, itemID int64)
	ExpandArtist func(ctx context.Context, subscriptionID int64, search bool)
}

// Search resolves categoryID to its media type and, for music, runs a
// free-text MusicBrainz release-group search (PriorityInteractive: a user is
// looking at this right now, ADR-29's own top priority).
func (a Discover) Search(
	ctx context.Context, categoryID int64, kind apiserver.DiscoverKind, query string, limit int,
) ([]apiserver.DiscoverResult, error) {
	if len([]rune(query)) < 2 {
		return nil, apiserver.ErrQueryTooShort
	}
	category, ok := a.Categories.Category(categoryID)
	if !ok || category.MediaType != "music" {
		return nil, apiserver.ErrDiscoverUnsupportedMediaType
	}
	if kind == apiserver.DiscoverArtist {
		return a.searchArtists(ctx, query, limit)
	}

	matches, err := a.Provider.SearchByQuery(ctx, musicbrainz.PriorityInteractive, query, limit)
	if err != nil {
		return nil, fmt.Errorf("musicbrainz: %w", err)
	}
	results := make([]apiserver.DiscoverResult, len(matches))
	for i, match := range matches {
		results[i] = apiserver.DiscoverResult{
			Kind: apiserver.DiscoverAlbum, Provider: music.ProviderReleaseGroup, ExternalID: match.MBID,
			Title: match.Title, Artist: match.ArtistCredit, Year: releaseYear(match.FirstReleaseDate),
		}
	}
	return results, nil
}

// creditArtists records an added album's artists at once, so a search before enrichment has an
// artist to ask for; enrichment fills in their details. Best-effort: enrichment credits them anyway.
func (a Discover) creditArtists(ctx context.Context, now time.Time, workID int64, credits []musicbrainz.ArtistCredit) {
	for index, credit := range credits {
		if credit.MBID == "" {
			continue
		}
		name := credit.ArtistName
		if name == "" {
			name = credit.Name
		}
		personID, err := a.People.ResolvePerson(ctx, now, library.PersonLink{
			Provider: music.ProviderArtist, ExternalID: credit.MBID,
			Person: library.PersonIdentity{Name: name},
		})
		if err != nil {
			continue
		}
		_ = a.People.AddCredit(ctx, workID, personID, music.CreditRoleArtist, index)
	}
}

// searchArtists is Search's artist half (revision 7.1). The disambiguation
// comes through because two artists sharing a name is the normal case, not an
// edge one, and the name alone cannot tell them apart.
func (a Discover) searchArtists(ctx context.Context, query string, limit int) ([]apiserver.DiscoverResult, error) {
	matches, err := a.Provider.SearchArtists(ctx, musicbrainz.PriorityInteractive, query, limit)
	if err != nil {
		return nil, fmt.Errorf("musicbrainz: %w", err)
	}
	results := make([]apiserver.DiscoverResult, len(matches))
	for i, match := range matches {
		results[i] = apiserver.DiscoverResult{
			Kind: apiserver.DiscoverArtist, Provider: music.ProviderArtist, ExternalID: match.MBID,
			Title: match.Name, Disambiguation: match.Disambiguation,
			Year: releaseYear(match.Born),
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
	ctx context.Context, categoryID, userID int64, provider, externalID string, monitored, search bool,
) (apiserver.DiscoverAddResult, error) {
	category, ok := a.Categories.Category(categoryID)
	if !ok || category.MediaType != "music" {
		return apiserver.DiscoverAddResult{}, apiserver.ErrDiscoverUnsupportedMediaType
	}
	if provider == music.ProviderArtist {
		return a.addArtist(ctx, category, userID, externalID, monitored, search)
	}
	if provider != music.ProviderReleaseGroup {
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

	a.creditArtists(ctx, now, workIDs[0], releaseGroup.ArtistCredit)

	profileID := a.DefaultProfileID
	if category.QualityProfileID != nil {
		profileID = *category.QualityProfileID
	}
	item, err := a.Desired.EnsureAlbumItem(ctx, library.AlbumRequest{
		WorkID: workIDs[0], CategoryID: category.ID, ProfileID: profileID, Monitored: monitored, AddedBy: &userID,
	})
	if err != nil {
		return apiserver.DiscoverAddResult{}, fmt.Errorf("ensure album item: %w", err)
	}

	score := priority.Compute(priority.DefaultWeights(), priority.Input{
		Trigger: priority.TriggerUserRequest, CategoryRank: category.PriorityRank, QueuedSince: now,
	}, now)
	if a.Enrich != nil {
		a.Enrich(ctx)
	}
	if monitored && search && a.SearchAlbum != nil {
		a.SearchAlbum(ctx, category.ID, item.ID)
	}
	return apiserver.DiscoverAddResult{
		Kind: apiserver.DiscoverAlbum, WorkID: workIDs[0], DesiredItemID: item.ID, Priority: score,
	}, nil
}

// addArtist creates or reuses the person behind an artist MBID and subscribes
// the caller to them (ADR-32). It deliberately creates no desired items: the
// discography arrives when subscription.expand runs, which is the mechanism
// that already decides per release group whether it is admitted. Idempotent
// twice over -- ResolvePerson returns an existing person untouched and
// Subscribe returns an existing subscription's id (Invariant 80).
func (a Discover) addArtist(
	ctx context.Context, category settings.Category, userID int64, externalID string, monitored, search bool,
) (apiserver.DiscoverAddResult, error) {
	// Re-fetched rather than trusted from the request, the same rule the album
	// path follows: an external id names identity, the provider says the rest.
	artist, err := a.Provider.Artist(ctx, musicbrainz.PriorityInteractive, externalID)
	if err != nil {
		return apiserver.DiscoverAddResult{}, fmt.Errorf("musicbrainz: %w", err)
	}

	now := a.Clock.Now()
	personID, err := a.People.ResolvePerson(ctx, now, library.PersonLink{
		Provider: music.ProviderArtist, ExternalID: externalID,
		Person: library.PersonIdentity{
			Name: artist.Name, Disambiguation: artist.Disambiguation,
			Kind: artist.Type, Gender: artist.Gender, Country: artist.Country,
			Born: artist.Born, Died: artist.Died,
		},
	})
	if err != nil {
		return apiserver.DiscoverAddResult{}, fmt.Errorf("resolve person: %w", err)
	}

	// Not monitored means "have them in the library without watching them", so
	// no subscription is created and nothing expands.
	var subscriptionID int64
	if monitored {
		filtersJSON, err := monitorFiltersFor(category)
		if err != nil {
			return apiserver.DiscoverAddResult{}, err
		}
		subscriptionID, err = a.People.Subscribe(ctx, now, personID, userID, library.SubscriptionRequest{
			MediaTypes: []string{"music"}, MonitorBackCatalogue: true,
			CategoryID: category.ID, FiltersJSON: filtersJSON,
		})
		if err != nil {
			return apiserver.DiscoverAddResult{}, fmt.Errorf("subscribe: %w", err)
		}
		if a.ExpandArtist != nil {
			a.ExpandArtist(ctx, subscriptionID, search)
		}
	}

	score := priority.Compute(priority.DefaultWeights(), priority.Input{
		Trigger: priority.TriggerUserRequest, CategoryRank: category.PriorityRank, QueuedSince: now,
	}, now)
	return apiserver.DiscoverAddResult{
		Kind: apiserver.DiscoverArtist, PersonID: personID, SubscriptionID: subscriptionID,
		Priority: score,
	}, nil
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
