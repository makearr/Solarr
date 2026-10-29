package adapters

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// Discover is music's media.Adding over musicbrainz.Provider and library.Identity/Desired: albums become wanted
// items, artists followed people (ADR-56's per-library half).
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

// LookupWorks implements media.Adding: a free-text MusicBrainz search of release groups or artists
// (PriorityInteractive: a person is looking at this right now, ADR-29's own top priority).
func (a Discover) LookupWorks(ctx context.Context, lookup media.AddLookup) ([]media.AddCandidate, error) {
	if len([]rune(lookup.Query)) < 2 {
		return nil, apiserver.ErrQueryTooShort
	}
	ctx = musicbrainz.ForLibrary(ctx, lookup.CategoryID)
	if lookup.Kind == "artist" {
		return a.searchArtists(ctx, lookup.Query, lookup.Limit)
	}
	matches, err := a.Provider.SearchByQuery(ctx, musicbrainz.PriorityInteractive, lookup.Query, lookup.Limit)
	if err != nil {
		return nil, fmt.Errorf("musicbrainz: %w", err)
	}
	results := make([]media.AddCandidate, len(matches))
	for i, match := range matches {
		results[i] = media.AddCandidate{
			Kind: "album", Provider: music.ProviderReleaseGroup, ExternalID: match.MBID,
			Title: match.Title, Subtitle: match.ArtistCredit, Year: releaseYear(match.FirstReleaseDate),
		}
		workID, found, err := a.Identity.WorkByExternalID(ctx, music.ProviderReleaseGroup, match.MBID)
		if err != nil {
			return nil, err
		}
		if found {
			results[i].LibraryID = workID
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
		if index == 0 {
			_ = a.People.AddLeadCredit(ctx, workID, personID, music.CreditRoleArtist)
		} else {
			_ = a.People.AddCredit(ctx, workID, personID, music.CreditRoleArtist, index)
		}
	}
}

// searchArtists is LookupWorks' artist half (revision 7.1). The disambiguation comes through because two artists
// sharing a name is the normal case, and the name alone cannot tell them apart.
func (a Discover) searchArtists(ctx context.Context, query string, limit int) ([]media.AddCandidate, error) {
	matches, err := a.Provider.SearchArtists(ctx, musicbrainz.PriorityInteractive, query, limit)
	if err != nil {
		return nil, fmt.Errorf("musicbrainz: %w", err)
	}
	results := make([]media.AddCandidate, len(matches))
	for i, match := range matches {
		results[i] = media.AddCandidate{
			Kind: "artist", Provider: music.ProviderArtist, ExternalID: match.MBID,
			Title: match.Name, Subtitle: match.Disambiguation, Year: releaseYear(match.Born),
		}
		personID, found, err := a.People.FindByExternalID(ctx, music.ProviderArtist, match.MBID)
		if err != nil {
			return nil, err
		}
		if found {
			results[i].LibraryID = personID
		}
	}
	return results, nil
}

// musicbrainzIDShape is the shape of an MBID; a mistyped one never reaches the source, which answers 400 for it.
var musicbrainzIDShape = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func checkMusicBrainzID(id string) error {
	if !musicbrainzIDShape.MatchString(id) {
		return fmt.Errorf("%w: %q is not a MusicBrainz id", apiserver.ErrCandidateNotFound, id)
	}
	return nil
}

// AddWork implements media.Adding: an album is resolved or created and gets a wanted item, without an edition yet
// (enrichment chooses one); an artist is followed.
func (a Discover) AddWork(ctx context.Context, request media.AddRequest) (media.AddResult, error) {
	category, ok := a.Categories.Category(request.CategoryID)
	if !ok {
		return media.AddResult{}, apiserver.ErrDiscoverUnsupportedMediaType
	}
	ctx = musicbrainz.ForLibrary(ctx, category.ID)
	userID, externalID, monitored, search := request.UserID, request.ExternalID, request.Monitored, request.Search
	if request.Provider == music.ProviderArtist {
		return a.addArtist(ctx, category, userID, externalID, monitored, search)
	}
	if request.Provider != music.ProviderReleaseGroup {
		return media.AddResult{}, apiserver.ErrDiscoverUnsupportedMediaType
	}

	// Re-fetched from the provider, never trusted from the request: an
	// external id names identity (ADR-8), the title that goes with it is the
	// provider's to say, not the caller's.
	if err := checkMusicBrainzID(externalID); err != nil {
		return media.AddResult{}, err
	}
	releaseGroup, _, err := a.Provider.Album(ctx, musicbrainz.PriorityInteractive, externalID, "")
	if errors.Is(err, musicbrainz.ErrNotFound) {
		return media.AddResult{}, fmt.Errorf("%w: release group %s", apiserver.ErrCandidateNotFound, externalID)
	}
	if err != nil {
		return media.AddResult{}, fmt.Errorf("musicbrainz: %w", err)
	}

	now := a.Clock.Now()
	workIDs, err := a.Identity.ResolveWorks(ctx, now, []library.WorkLink{{
		Provider: music.ProviderReleaseGroup, ExternalID: externalID, Confidence: 1.0,
		Work: library.WorkIdentity{
			MediaType: "music", CanonicalTitle: releaseGroup.Title, SortTitle: music.SortTitle(releaseGroup.Title),
		},
	}})
	if err != nil {
		return media.AddResult{}, fmt.Errorf("resolve work: %w", err)
	}

	a.creditArtists(ctx, now, workIDs[0], releaseGroup.ArtistCredit)

	profileID := a.DefaultProfileID
	if category.QualityProfileID != nil {
		profileID = *category.QualityProfileID
	}
	if request.ProfileID != nil {
		profileID = *request.ProfileID
	}
	item, err := a.Desired.EnsureAlbumItem(ctx, library.AlbumRequest{
		WorkID: workIDs[0], CategoryID: category.ID, ProfileID: profileID, Monitored: monitored, AddedBy: &userID,
	})
	if err != nil {
		return media.AddResult{}, fmt.Errorf("ensure album item: %w", err)
	}
	if a.Enrich != nil {
		a.Enrich(ctx)
	}
	searching := monitored && search && a.SearchAlbum != nil
	if searching {
		a.SearchAlbum(ctx, category.ID, item.ID)
	}
	return media.AddResult{Kind: "album", WorkID: workIDs[0], Searching: searching}, nil
}

// addArtist creates or reuses the person behind an artist MBID and subscribes
// the caller to them (ADR-32). It deliberately creates no desired items: the
// discography arrives when subscription.expand runs, which is the mechanism
// that already decides per release group whether it is admitted. Idempotent
// twice over -- ResolvePerson returns an existing person untouched and
// Subscribe returns an existing subscription's id (Invariant 80).
func (a Discover) addArtist(
	ctx context.Context, category settings.Category, userID int64, externalID string, monitored, search bool,
) (media.AddResult, error) {
	// Re-fetched rather than trusted from the request, the same rule the album
	// path follows: an external id names identity, the provider says the rest.
	if err := checkMusicBrainzID(externalID); err != nil {
		return media.AddResult{}, err
	}
	artist, err := a.Provider.Artist(ctx, musicbrainz.PriorityInteractive, externalID)
	if errors.Is(err, musicbrainz.ErrNotFound) {
		return media.AddResult{}, fmt.Errorf("%w: artist %s", apiserver.ErrCandidateNotFound, externalID)
	}
	if err != nil {
		return media.AddResult{}, fmt.Errorf("musicbrainz: %w", err)
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
		return media.AddResult{}, fmt.Errorf("resolve person: %w", err)
	}

	// Not monitored means "have them in the library without watching them", so
	// no subscription is created and nothing expands.
	var subscriptionID int64
	if monitored {
		filtersJSON, err := monitorFiltersFor(category)
		if err != nil {
			return media.AddResult{}, err
		}
		subscriptionID, err = a.People.Subscribe(ctx, now, personID, userID, library.SubscriptionRequest{
			MediaTypes: []string{"music"}, MonitorBackCatalogue: true,
			CategoryID: category.ID, FiltersJSON: filtersJSON,
		})
		if err != nil {
			return media.AddResult{}, fmt.Errorf("subscribe: %w", err)
		}
		if a.ExpandArtist != nil {
			a.ExpandArtist(ctx, subscriptionID, search)
		}
	}

	return media.AddResult{Kind: "artist", PersonID: personID, Searching: monitored && search}, nil
}

// releaseYear reads only the leading year off a MusicBrainz partial date
// ("1978", "1978-01", "1978-01-12") -- Discover's result list shows a year
// for context, not the day precision todo 4.20's own FreshSince cares about.
func releaseYear(date string) int64 {
	if len(date) < 4 {
		return 0
	}
	year, _ := strconv.ParseInt(date[:4], 10, 64)
	return year
}
