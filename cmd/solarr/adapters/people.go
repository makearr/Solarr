package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/listfilter"
	"github.com/makearr/Solarr/internal/platform/paginate"
)

// People satisfies apiserver.PeopleReader, apiserver.PeopleSubscriber and
// apiserver.SubscriptionPreviewer over library.People. Provider is only
// needed for Preview (todo 4.4) -- ListPeople/GetPersonDetail/Subscribe
// never touch MusicBrainz directly.
type People struct {
	People library.People
	// Roles answers every credit role the media types declare, for a custom filter's role choices.
	Roles         func() []string
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
	if _, err := a.People.Person(ctx, personID); err != nil {
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

// ListPeople maps one page of the one people list.
func (a People) ListPeople(ctx context.Context, query apiserver.PeopleQuery, after paginate.TextCursor, limit int) ([]gen.PersonEntry, error) {
	where, err := listfilter.Parse(query.Where, library.PersonFields(a.Roles()))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", apiserver.ErrInvalidChoice, err)
	}
	cursor := library.PeopleCursor{ID: after.ID, Set: after.Sort != "" || after.ID != 0}
	if query.Sort == library.PeopleByName || query.Sort == "" {
		cursor.Name = after.Sort
	} else {
		cursor.Value, _ = strconv.ParseInt(after.Sort, 10, 64)
	}
	rows, err := a.People.List(ctx, library.PeopleFilter{
		Visible: query.Visible, Category: query.Category, Roles: query.Roles, Monitored: query.Monitored,
		NamePrefix: query.Prefix, Where: where, Sort: query.Sort, Desc: query.Desc,
	}, cursor, limit)
	if err != nil {
		return nil, err
	}
	views := make([]gen.PersonEntry, len(rows))
	for i, row := range rows {
		view := gen.PersonEntry{
			Id: row.ID, Name: row.Name, SortName: row.SortName, Monitored: row.Monitored, Roles: row.Roles,
			Works: row.Works, Wanted: row.Wanted, Complete: row.Complete, Downloading: row.Downloading, AddedAt: &row.AddedAt,
		}
		if view.Roles == nil {
			view.Roles = []string{}
		}
		if row.Latest > 0 {
			latest := int(row.Latest)
			view.Latest = &latest
		}
		setString(&view.Disambiguation, row.Disambiguation)
		setString(&view.Kind, row.Kind)
		setString(&view.Country, row.Country)
		setString(&view.Born, row.Born)
		setString(&view.Died, row.Died)
		setString(&view.ImageUrl, row.ImageURL)
		setString(&view.Character, row.Character)
		views[i] = view
	}
	return views, nil
}

// GetPersonDetail maps one person and their credits in the libraries visible holds.
func (a People) GetPersonDetail(ctx context.Context, id int64, visible []int64) (gen.PersonDetail, []apiserver.PersonCreditRow, error) {
	profile, err := a.People.Get(ctx, id, visible)
	if errors.Is(err, library.ErrPersonNotFound) {
		return gen.PersonDetail{}, nil, fmt.Errorf("%w: %w", apiserver.ErrPersonNotFound, err)
	}
	if err != nil {
		return gen.PersonDetail{}, nil, err
	}
	credits, err := a.People.Credits(ctx, id, visible)
	if err != nil {
		return gen.PersonDetail{}, nil, err
	}
	view := gen.PersonDetail{
		Id: profile.ID, Name: profile.Name, SortName: profile.SortName, Monitored: profile.Monitored,
		Aliases: nonNil(profile.Aliases), Genres: nonNil(profile.Genres), Ids: profile.IDs,
		Libraries: make([]gen.PersonLibrary, len(profile.Libraries)),
	}
	for i, place := range profile.Libraries {
		view.Libraries[i] = gen.PersonLibrary{CategoryId: place.CategoryID, Role: place.Role, Works: place.Works}
	}
	setString(&view.Disambiguation, profile.Disambiguation)
	setString(&view.Kind, profile.Kind)
	setString(&view.Gender, profile.Gender)
	setString(&view.Country, profile.Country)
	setString(&view.Born, profile.Born)
	setString(&view.Died, profile.Died)
	setString(&view.ImageUrl, profile.ImageURL)
	setString(&view.Biography, profile.Biography)
	rows := make([]apiserver.PersonCreditRow, len(credits))
	for i, credit := range credits {
		rows[i] = apiserver.PersonCreditRow{
			CategoryID: credit.CategoryID, Role: credit.Role, Character: credit.Character, Parts: credit.Parts,
			Work: personWorkRow(credit.DiscographyRow),
		}
	}
	return view, rows, nil
}

// personWorkRow maps a credited work with the facts its status colour is decided from.
func personWorkRow(work library.DiscographyRow) apiserver.PersonWorkRow {
	row := apiserver.PersonWorkRow{
		ID: work.ID, MediaType: work.MediaType, Title: work.CanonicalTitle, SortTitle: work.SortTitle,
		OnDisk: work.OnDiskCount > 0, Downloading: work.Downloading, Unverified: work.Unverified,
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
	return row
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func (a People) Subscribe(
	ctx context.Context, personID, userID, categoryID int64, mediaTypes, roles []string,
	monitorBackCatalogue bool, includePrimaryTypes, excludeSecondaryTypes *[]string,
) error {
	// Absent primary types mean the library's own setting, resolved here and
	// stored on the subscription: the artist keeps what was chosen when they
	// were added, even if the library's default moves later.
	category, haveCategory := a.Categories.Category(categoryID)
	if includePrimaryTypes == nil && haveCategory {
		types := category.MonitorReleaseTypes
		includePrimaryTypes = &types
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
	request := library.SubscriptionRequest{
		MediaTypes: mediaTypes, Roles: roles, MonitorBackCatalogue: monitorBackCatalogue,
		FiltersJSON: filtersJSON,
	}
	if haveCategory {
		request.CategoryID = category.ID
	}
	_, err := a.People.Subscribe(ctx, a.Clock.Now(), personID, userID, request)
	return err
}

// monitorFiltersFor encodes a library's own release-type default onto a
// subscription, so what an artist brings in is fixed when they are added
// rather than moving with the library's setting afterwards.
func monitorFiltersFor(category settings.Category) (string, error) {
	types := category.MonitorReleaseTypes
	encoded, err := json.Marshal(music.SubscriptionFilters{IncludePrimaryTypes: &types})
	if err != nil {
		return "", fmt.Errorf("adapters: encode subscription filters: %w", err)
	}
	return string(encoded), nil
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
