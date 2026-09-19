package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// People satisfies apiserver.PeopleReader, apiserver.PeopleSubscriber and
// apiserver.SubscriptionPreviewer over library.People. Provider is only
// needed for Preview (todo 4.4) -- ListPeople/GetPersonDetail/Subscribe
// never touch MusicBrainz directly.
type People struct {
	People        library.People
	Subscriptions library.Subscriptions
	Provider      musicbrainz.Provider
	Categories    Categories
	Clock         clock.Clock
}

// Remove stops following an artist and deletes them (revision 7.3). The
// subscription goes first so a half-finished removal leaves a person who is
// merely no longer followed, rather than a subscription pointing at a person
// who no longer exists. Files are never touched: a work belongs to the
// library, not to the person credited on it.
func (a People) Remove(ctx context.Context, personID, userID int64) error {
	if _, err := a.People.Detail(ctx, personID); err != nil {
		return err
	}
	subscriptionID, found, err := a.Subscriptions.Find(ctx, "person", personID, userID)
	if err != nil {
		return fmt.Errorf("adapters: find subscription for person %d: %w", personID, err)
	}
	if found {
		if err := a.Subscriptions.Delete(ctx, subscriptionID); err != nil {
			return fmt.Errorf("adapters: delete subscription %d: %w", subscriptionID, err)
		}
	}
	return a.People.Delete(ctx, personID)
}

func (a People) ListPeople(
	ctx context.Context, filter apiserver.PersonFilter, cursor apiserver.PersonCursor, pageSize int,
) ([]apiserver.PersonRow, error) {
	rows, err := a.People.ListPeople(ctx,
		library.PersonFilter{Monitored: filter.Monitored, NamePrefix: filter.NamePrefix},
		library.PersonCursor{SortName: cursor.SortName, ID: cursor.ID}, pageSize)
	if err != nil {
		return nil, err
	}

	views := make([]apiserver.PersonRow, len(rows))
	for index, row := range rows {
		view := apiserver.PersonRow{ID: row.ID, Name: row.Name, SortName: row.SortName, Monitored: row.Monitored != 0}
		if row.Disambiguation.Valid {
			view.Disambiguation = &row.Disambiguation.String
		}
		if row.Kind.Valid {
			view.Kind = &row.Kind.String
		}
		if row.Gender.Valid {
			view.Gender = &row.Gender.String
		}
		if row.Country.Valid {
			view.Country = &row.Country.String
		}
		if row.Born.Valid {
			view.Born = &row.Born.String
		}
		if row.Died.Valid {
			view.Died = &row.Died.String
		}
		views[index] = view
	}
	return views, nil
}

func (a People) GetPersonDetail(ctx context.Context, id int64) (apiserver.PersonDetailRow, error) {
	detail, err := a.People.Detail(ctx, id)
	if err != nil {
		return apiserver.PersonDetailRow{}, err
	}

	discography := make([]apiserver.PersonWorkRow, len(detail.Discography))
	for index, work := range detail.Discography {
		row := apiserver.PersonWorkRow{
			ID: work.ID, MediaType: work.MediaType, Title: work.CanonicalTitle, SortTitle: work.SortTitle,
			OnDisk: work.OnDiskCount > 0,
		}
		if work.Year.Valid {
			row.Year = &work.Year.Int64
		}
		if work.PrimaryType.Valid {
			row.PrimaryType = &work.PrimaryType.String
		}
		if work.WantedState.Valid {
			row.WantedState = &work.WantedState.String
		}
		if work.PresentCount.Valid {
			row.PresentCount = &work.PresentCount.Int64
		}
		if work.RequiredCount.Valid {
			row.RequiredCount = &work.RequiredCount.Int64
		}
		if work.AvailableFrom.Valid {
			at := time.Unix(work.AvailableFrom.Int64, 0).UTC()
			row.AvailableFrom = &at
		}
		row.Downloading = work.Downloading
		discography[index] = row
	}

	person := detail.Person
	view := apiserver.PersonDetailRow{
		PersonRow: apiserver.PersonRow{
			ID: person.ID, Name: person.Name, SortName: person.SortName, Monitored: person.Monitored != 0,
		},
		Genres: detail.Genres, Discography: discography,
	}
	if person.Disambiguation.Valid {
		view.Disambiguation = &person.Disambiguation.String
	}
	if person.Kind.Valid {
		view.Kind = &person.Kind.String
	}
	if person.Gender.Valid {
		view.Gender = &person.Gender.String
	}
	if person.Country.Valid {
		view.Country = &person.Country.String
	}
	if person.Born.Valid {
		view.Born = &person.Born.String
	}
	if person.Died.Valid {
		view.Died = &person.Died.String
	}
	if detail.Biography != "" {
		view.Biography = &detail.Biography
	}
	// Unconditional, unlike the optional fields above: aliases is a required
	// array in the OpenAPI schema, so it must serialize as [] rather than
	// null for a person with none -- a guarded assignment here left the Go
	// zero value (a nil slice) in place, which encodes as JSON null and
	// crashed the web UI's own person.aliases.length (live-verified
	// 2026-09-12: "The 1987" has none, PersonDetail.tsx threw a TypeError).
	view.Aliases = detail.Aliases
	return view, nil
}

func (a People) Subscribe(
	ctx context.Context, personID, userID int64, mediaTypes, roles []string,
	monitorBackCatalogue bool, includePrimaryTypes, excludeSecondaryTypes *[]string,
) error {
	// Absent primary types mean the library's own setting, resolved here and
	// stored on the subscription: the artist keeps what was chosen when they
	// were added, even if the library's default moves later.
	if includePrimaryTypes == nil {
		if category, ok := a.Categories.CategoryForMediaType(mediaTypeOrMusic(mediaTypes)); ok {
			types := category.MonitorReleaseTypes
			includePrimaryTypes = &types
		}
	}

	var filtersJSON string
	if includePrimaryTypes != nil || excludeSecondaryTypes != nil {
		encoded, err := json.Marshal(music.SubscriptionFilters{
			IncludePrimaryTypes: includePrimaryTypes, ExcludeSecondaryTypes: excludeSecondaryTypes,
		})
		if err != nil {
			return fmt.Errorf("adapters: encode subscription filters: %w", err)
		}
		filtersJSON = string(encoded)
	}
	_, err := a.People.Subscribe(ctx, a.Clock.Now(), personID, userID, library.SubscriptionRequest{
		MediaTypes: mediaTypes, Roles: roles, MonitorBackCatalogue: monitorBackCatalogue,
		FiltersJSON: filtersJSON,
	})
	return err
}

// Preview answers an artist's discography unfiltered (todo 4.4), the same
// external-id lookup and provider call music.SubscriptionExpandJob's own
// expandOne makes -- apiserver.ErrNoArtistID mirrors that job's "person has
// no external id yet" error, mapped to a client-facing 409 here since a
// background sweep just retries next run, but a screen has to say something
// now.
func (a People) Preview(ctx context.Context, personID int64) (apiserver.SubscriptionPreviewRow, error) {
	artistMBID, ok, err := a.People.ExternalIDFor(ctx, personID, music.ProviderArtist)
	if err != nil {
		return apiserver.SubscriptionPreviewRow{}, err
	}
	if !ok {
		return apiserver.SubscriptionPreviewRow{}, apiserver.ErrNoArtistID
	}

	groups, err := a.Provider.ArtistReleaseGroups(ctx, musicbrainz.PriorityInteractive, artistMBID)
	if err != nil {
		return apiserver.SubscriptionPreviewRow{}, fmt.Errorf("musicbrainz: %w", err)
	}

	rows := make([]apiserver.PreviewReleaseGroupRow, len(groups))
	for index, group := range groups {
		rows[index] = apiserver.PreviewReleaseGroupRow{
			MBID: group.MBID, Title: group.Title, PrimaryType: group.PrimaryType,
			FirstReleaseDate: group.FirstReleaseDate, SecondaryTypes: group.SecondaryTypes,
		}
	}
	return apiserver.SubscriptionPreviewRow{
		ReleaseGroups: rows, DefaultExcludedSecondaryTypes: music.DefaultExcludedSecondaryTypes,
	}, nil
}

// mediaTypeOrMusic picks the media type whose library setting a subscription
// takes its release types from. One subscription can name several; music is
// the only one with a release-type vocabulary today.
func mediaTypeOrMusic(mediaTypes []string) string {
	for _, mediaType := range mediaTypes {
		if mediaType != "" {
			return mediaType
		}
	}
	return "music"
}
