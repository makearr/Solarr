package music

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// ErrFollowNotWired reports a music module built without what following an artist needs.
var ErrFollowNotWired = errors.New("music: following is not wired")

// ErrNoArtistID reports an artist with no MusicBrainz id yet: there is no discography to browse until one exists.
var ErrNoArtistID = fmt.Errorf("%w: the artist has no MusicBrainz id yet", media.ErrNoFollowSource)

// ArtistSearch searches one artist's missing albums in one library, after their albums were added.
type ArtistSearch func(ctx context.Context, categoryID, personID int64) error

// Discography is the provider as following an artist needs it.
type Discography interface {
	ArtistReleaseGroups(ctx context.Context, priority musicbrainz.Priority, artistMBID string) ([]musicbrainz.ReleaseGroup, error)
}

// Following is music's part of a follow: an artist's release groups, as the follow's options admit (section 19.6).
type Following struct {
	People           library.People
	Identity         library.Identity
	Desired          *library.Desired
	Categories       CategoryFinder
	Discography      Discography
	DefaultProfileID int64
	Clock            clock.Clock
	// Search is optional; without it a follow asking for a search only adds.
	Search ArtistSearch
}

// followOptions are what a music follow offers; the keys are SubscriptionFilters' own, so stored follows keep reading.
var followOptions = []media.FollowOption{
	{
		Key: "include_primary_types", Label: "Release types", Many: true, Default: DefaultIncludedPrimaryTypes,
		Choices: []media.Choice{{Key: "Album", Label: "Albums"}, {Key: "EP", Label: "EPs"}, {Key: "Single", Label: "Singles"},
			{Key: "Broadcast", Label: "Broadcasts"}, {Key: "Other", Label: "Other"}},
	},
	{
		Key: "exclude_secondary_types", Label: "Leave out", Many: true, Default: DefaultExcludedSecondaryTypes,
		Choices: []media.Choice{{Key: "compilation", Label: "Compilations"}, {Key: "live", Label: "Live"}, {Key: "remix", Label: "Remixes"},
			{Key: "dj-mix", Label: "DJ mixes"}, {Key: "mixtape", Label: "Mixtapes"}, {Key: "soundtrack", Label: "Soundtracks"},
			{Key: "demo", Label: "Demos"}, {Key: "spokenword", Label: "Spoken word"}, {Key: "interview", Label: "Interviews"}},
	},
}

// FollowLimit implements media.Following: an artist's follow brings their whole admitted discography.
func (Following) FollowLimit(bool) int { return 0 }

// FollowCandidates implements media.Following: the artist's release groups the follow's options admit, newest first.
func (f Following) FollowCandidates(ctx context.Context, follow media.Follow) ([]media.FollowCandidate, error) {
	if f.Discography == nil || f.Desired == nil {
		return nil, ErrFollowNotWired
	}
	if follow.SubjectType != "person" {
		return nil, nil
	}
	artistMBID, ok, err := f.People.ExternalIDFor(ctx, follow.SubjectID, ProviderArtist)
	if err != nil {
		return nil, fmt.Errorf("music: artist id: %w", err)
	}
	if !ok {
		return nil, ErrNoArtistID
	}
	groups, err := f.Discography.ArtistReleaseGroups(ctx, musicbrainz.PriorityBackfill, artistMBID)
	if err != nil {
		return nil, fmt.Errorf("musicbrainz: %w", err)
	}
	filters := ParseSubscriptionFilters(follow.FiltersJSON)
	if category, ok := f.Categories.Category(follow.CategoryID); ok && filters.IncludePrimaryTypes == nil && category.MonitorReleaseTypes != nil {
		// A follow naming no release types takes its library's own (2026-09-19).
		types := category.MonitorReleaseTypes
		filters.IncludePrimaryTypes = &types
	}
	var candidates []media.FollowCandidate
	for _, group := range groups {
		if !filters.Admits(group) {
			continue
		}
		held, err := f.held(ctx, group.MBID, follow.CategoryID)
		if err != nil {
			return nil, err
		}
		year, _ := strconv.ParseInt(strings.SplitN(group.FirstReleaseDate, "-", 2)[0], 10, 64)
		candidates = append(candidates, media.FollowCandidate{
			Provider: ProviderReleaseGroup, ExternalID: group.MBID, Title: group.Title, Year: year, Role: CreditRoleArtist, Held: held,
		})
	}
	slices.SortStableFunc(candidates, func(a, b media.FollowCandidate) int { return int(b.Year - a.Year) })
	return candidates, nil
}

// held reports whether the library already wants the release group as an album, monitored or not.
func (f Following) held(ctx context.Context, mbid string, categoryID int64) (bool, error) {
	workID, found, err := f.Identity.WorkByExternalID(ctx, ProviderReleaseGroup, mbid)
	if err != nil || !found {
		return false, err
	}
	return f.Desired.WantedIn(ctx, workID, categoryID)
}

// AddFollowed implements media.Following: the release group becomes a work with a monitored album item.
func (f Following) AddFollowed(ctx context.Context, follow media.Follow, candidate media.FollowCandidate) error {
	if f.Desired == nil {
		return ErrFollowNotWired
	}
	category, ok := f.Categories.Category(follow.CategoryID)
	if !ok {
		category, ok = f.Categories.CategoryForMediaType(string(MediaType))
	}
	if !ok {
		return fmt.Errorf("music: no library for the follow")
	}
	profileID := f.DefaultProfileID
	if category.QualityProfileID != nil {
		profileID = *category.QualityProfileID
	}
	workIDs, err := f.Identity.ResolveWorks(ctx, f.Clock.Now(), []library.WorkLink{{
		Provider: ProviderReleaseGroup, ExternalID: candidate.ExternalID, Confidence: 1.0,
		Work: library.WorkIdentity{MediaType: string(MediaType), CanonicalTitle: candidate.Title, SortTitle: SortTitle(candidate.Title)},
	}})
	if err != nil {
		return fmt.Errorf("music: resolve work: %w", err)
	}
	_, err = f.Desired.EnsureAlbumItem(ctx, library.AlbumRequest{WorkID: workIDs[0], CategoryID: category.ID, ProfileID: profileID, Monitored: true})
	if err != nil {
		return fmt.Errorf("music: ensure album item: %w", err)
	}
	return nil
}

// SearchFollowed implements media.FollowSearching: the artist's missing albums in the follow's library.
func (f Following) SearchFollowed(ctx context.Context, follow media.Follow) error {
	if f.Search == nil || follow.SubjectType != "person" {
		return nil
	}
	return f.Search(ctx, follow.CategoryID, follow.SubjectID)
}
