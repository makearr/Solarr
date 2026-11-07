package music

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// Add kinds: an album becomes a wanted work, an artist a followed person.
const (
	AddKindAlbum  = "album"
	AddKindArtist = "artist"
)

// Adder is music's media.Adding over MusicBrainz: albums become wanted items, artists followed people (ADR-56).
type Adder struct {
	Provider         musicbrainz.Provider
	Identity         library.Identity
	Desired          library.Desired
	People           library.People
	Categories       Categories
	DefaultProfileID int64
	Clock            clock.Clock
	// Enrich starts fetching an added album's tracklist now rather than at the next hourly sweep; nil waits.
	Enrich func(context.Context)
	// SearchAlbum searches one added album at once; ExpandArtist adds a new artist's albums now. Nil does neither.
	SearchAlbum  func(ctx context.Context, categoryID, itemID int64)
	ExpandArtist func(ctx context.Context, subscriptionID int64, search bool)
}

// LookupWorks implements media.Adding: a free-text MusicBrainz search of release groups or artists, at interactive
// priority since a person is waiting on it (ADR-29).
func (a Adder) LookupWorks(ctx context.Context, lookup media.AddLookup) ([]media.AddCandidate, error) {
	if len([]rune(lookup.Query)) < 2 {
		return nil, media.ErrQueryTooShort
	}
	ctx = musicbrainz.ForLibrary(ctx, lookup.CategoryID)
	if lookup.Kind == AddKindArtist {
		return a.searchArtists(ctx, lookup.Query, lookup.Limit)
	}
	matches, err := a.Provider.SearchByQuery(ctx, musicbrainz.PriorityInteractive, lookup.Query, lookup.Limit)
	if err != nil {
		return nil, fmt.Errorf("musicbrainz: %w", err)
	}
	results := make([]media.AddCandidate, len(matches))
	for i, match := range matches {
		results[i] = media.AddCandidate{
			Kind: AddKindAlbum, Provider: ProviderReleaseGroup, ExternalID: match.MBID,
			Title: match.Title, Subtitle: match.ArtistCredit, Year: partialDateYear(match.FirstReleaseDate),
		}
		workID, found, err := a.Identity.WorkByExternalID(ctx, ProviderReleaseGroup, match.MBID)
		if err != nil {
			return nil, err
		}
		if found {
			results[i].LibraryID = workID
		}
	}
	return results, nil
}

// searchArtists is LookupWorks' artist half; the disambiguation tells apart the artists who share a name.
func (a Adder) searchArtists(ctx context.Context, query string, limit int) ([]media.AddCandidate, error) {
	matches, err := a.Provider.SearchArtists(ctx, musicbrainz.PriorityInteractive, query, limit)
	if err != nil {
		return nil, fmt.Errorf("musicbrainz: %w", err)
	}
	results := make([]media.AddCandidate, len(matches))
	for i, match := range matches {
		results[i] = media.AddCandidate{
			Kind: AddKindArtist, Provider: ProviderArtist, ExternalID: match.MBID,
			Title: match.Name, Subtitle: match.Disambiguation, Year: partialDateYear(match.Born),
		}
		personID, found, err := a.People.FindByExternalID(ctx, ProviderArtist, match.MBID)
		if err != nil {
			return nil, err
		}
		if found {
			results[i].LibraryID = personID
		}
	}
	return results, nil
}

// mbidShape is the shape of an MBID; a mistyped one never reaches the source, which answers 400 for it.
var mbidShape = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func checkMBID(id string) error {
	if !mbidShape.MatchString(id) {
		return fmt.Errorf("%w: %q is not a MusicBrainz id", media.ErrUnknownCandidate, id)
	}
	return nil
}

// AddWork implements media.Adding: an album is resolved or created and gets a wanted item, without an edition yet
// (enrichment chooses one); an artist is followed. The title is re-fetched, never taken from the request (ADR-8).
func (a Adder) AddWork(ctx context.Context, request media.AddRequest) (media.AddResult, error) {
	category, ok := a.Categories.Category(request.CategoryID)
	if !ok {
		return media.AddResult{}, fmt.Errorf("%w: no library %d", media.ErrInvalidAdd, request.CategoryID)
	}
	ctx = musicbrainz.ForLibrary(ctx, category.ID)
	switch request.Provider {
	case ProviderArtist:
		return a.addArtist(ctx, category, request)
	case ProviderReleaseGroup:
		return a.addAlbum(ctx, category, request)
	}
	return media.AddResult{}, fmt.Errorf("%w: music adds nothing by %q", media.ErrInvalidAdd, request.Provider)
}

func (a Adder) addAlbum(ctx context.Context, category settings.Category, request media.AddRequest) (media.AddResult, error) {
	if err := checkMBID(request.ExternalID); err != nil {
		return media.AddResult{}, err
	}
	releaseGroup, _, err := a.Provider.Album(ctx, musicbrainz.PriorityInteractive, request.ExternalID, "")
	if errors.Is(err, musicbrainz.ErrNotFound) {
		return media.AddResult{}, fmt.Errorf("%w: release group %s", media.ErrUnknownCandidate, request.ExternalID)
	}
	if err != nil {
		return media.AddResult{}, fmt.Errorf("musicbrainz: %w", err)
	}

	now := a.Clock.Now()
	resolutions, err := a.Identity.ResolveWorksDetailed(ctx, now, []library.WorkLink{AlbumLink(request.ExternalID, releaseGroup.Title)})
	if err != nil {
		return media.AddResult{}, fmt.Errorf("resolve work: %w", err)
	}
	workID := resolutions[0].WorkID
	a.creditArtists(ctx, now, workID, releaseGroup.ArtistCredit)

	profileID := a.DefaultProfileID
	if category.QualityProfileID != nil {
		profileID = *category.QualityProfileID
	}
	if request.ProfileID != nil {
		profileID = *request.ProfileID
	}
	userID := request.UserID
	item, err := a.Desired.EnsureAlbumItem(ctx, library.AlbumRequest{
		WorkID: workID, CategoryID: category.ID, ProfileID: profileID, Monitored: request.Monitored, AddedBy: &userID,
	})
	if err != nil {
		return media.AddResult{}, fmt.Errorf("ensure album item: %w", err)
	}
	if a.Enrich != nil {
		a.Enrich(ctx)
	}
	searching := request.Monitored && request.Search && a.SearchAlbum != nil
	if searching {
		a.SearchAlbum(ctx, category.ID, item.ID)
	}
	return media.AddResult{Kind: AddKindAlbum, WorkID: workID, Searching: searching, Created: resolutions[0].WorkCreated}, nil
}

// creditArtists records an added album's artists at once, so a search before enrichment has an artist to ask for.
// Best-effort: enrichment credits them anyway.
func (a Adder) creditArtists(ctx context.Context, now time.Time, workID int64, credits []musicbrainz.ArtistCredit) {
	for index, credit := range credits {
		if credit.MBID == "" {
			continue
		}
		name := credit.ArtistName
		if name == "" {
			name = credit.Name
		}
		personID, err := a.People.ResolvePerson(ctx, now, ArtistLink(credit.MBID, library.PersonIdentity{Name: name}))
		if err != nil {
			continue
		}
		if index == 0 {
			_ = a.People.AddLeadCredit(ctx, workID, personID, CreditRoleArtist)
		} else {
			_ = a.People.AddCredit(ctx, workID, personID, CreditRoleArtist, index)
		}
	}
}

// addArtist resolves the person behind an artist MBID and, when monitored, follows them; the discography arrives with
// the follow's expansion (ADR-32), which decides per release group what is admitted. Idempotent (Invariant 80).
func (a Adder) addArtist(ctx context.Context, category settings.Category, request media.AddRequest) (media.AddResult, error) {
	if err := checkMBID(request.ExternalID); err != nil {
		return media.AddResult{}, err
	}
	artist, err := a.Provider.Artist(ctx, musicbrainz.PriorityInteractive, request.ExternalID)
	if errors.Is(err, musicbrainz.ErrNotFound) {
		return media.AddResult{}, fmt.Errorf("%w: artist %s", media.ErrUnknownCandidate, request.ExternalID)
	}
	if err != nil {
		return media.AddResult{}, fmt.Errorf("musicbrainz: %w", err)
	}

	now := a.Clock.Now()
	personID, err := a.People.ResolvePerson(ctx, now, ArtistLink(request.ExternalID, library.PersonIdentity{
		Name: artist.Name, Disambiguation: artist.Disambiguation,
		Kind: artist.Type, Gender: artist.Gender, Country: artist.Country,
		Born: artist.Born, Died: artist.Died,
	}))
	if err != nil {
		return media.AddResult{}, fmt.Errorf("resolve person: %w", err)
	}

	// Not monitored means "in the library without watching them": no follow, nothing expands.
	if request.Monitored {
		filtersJSON, err := LibraryFollowFilters(category)
		if err != nil {
			return media.AddResult{}, err
		}
		subscriptionID, err := a.People.Subscribe(ctx, now, personID, request.UserID, library.SubscriptionRequest{
			MediaTypes: []string{string(MediaType)}, MonitorBackCatalogue: true,
			CategoryID: category.ID, FiltersJSON: filtersJSON,
		})
		if err != nil {
			return media.AddResult{}, fmt.Errorf("subscribe: %w", err)
		}
		if a.ExpandArtist != nil {
			a.ExpandArtist(ctx, subscriptionID, request.Search)
		}
	}
	return media.AddResult{Kind: AddKindArtist, PersonID: personID, Searching: request.Monitored && request.Search}, nil
}

// AlbumLink is the identity of an album by its release-group MBID, as every music path that creates one states it.
func AlbumLink(releaseGroupMBID, title string) library.WorkLink {
	return library.WorkLink{
		Provider: ProviderReleaseGroup, ExternalID: releaseGroupMBID, Confidence: 1.0,
		Work: library.WorkIdentity{MediaType: string(MediaType), CanonicalTitle: title, SortTitle: library.SortTitle(title)},
	}
}

// ArtistLink is the identity of an artist by their MBID.
func ArtistLink(artistMBID string, person library.PersonIdentity) library.PersonLink {
	return library.PersonLink{Provider: ProviderArtist, ExternalID: artistMBID, Person: person}
}

// LibraryFollowFilters fixes a library's release-type default onto a new follow, so what an artist brings in does not
// move with the library's setting afterwards.
func LibraryFollowFilters(category settings.Category) (string, error) {
	types := category.MonitorReleaseTypes
	encoded, err := json.Marshal(SubscriptionFilters{IncludePrimaryTypes: &types})
	if err != nil {
		return "", fmt.Errorf("music: encode follow filters: %w", err)
	}
	return string(encoded), nil
}

// partialDateYear reads the year off a MusicBrainz partial date ("1978", "1978-01", "1978-01-12"); 0 when it has none.
func partialDateYear(date string) int64 {
	if len(date) < 4 {
		return 0
	}
	year, _ := strconv.ParseInt(date[:4], 10, 64)
	return year
}
