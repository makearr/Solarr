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
	"github.com/makearr/Solarr/internal/media"
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
	Works  library.Works
	// Profiling answers the media type of a library as a profiling one, when it is.
	Profiling func(categoryID int64) (media.Profiling, bool)
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
	follows, err := a.Subscriptions.FollowsOf(ctx, library.Subject{Type: "person", ID: personID}, userID)
	if err != nil {
		return fmt.Errorf("adapters: follows of person %d: %w", personID, err)
	}
	for _, followed := range follows {
		if err := a.Subscriptions.Delete(ctx, followed.ID); err != nil {
			return fmt.Errorf("adapters: delete follow %d: %w", followed.ID, err)
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
		if row.NoArtwork {
			view.NoArtwork = &row.NoArtwork
		}
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
	ids := make([]int64, len(credits))
	for i, credit := range credits {
		ids[i] = credit.ID
	}
	posters, err := a.Works.Posters(ctx, ids)
	if err != nil {
		return gen.PersonDetail{}, nil, err
	}
	rows := make([]apiserver.PersonCreditRow, len(credits))
	for i, credit := range credits {
		rows[i] = apiserver.PersonCreditRow{
			CategoryID: credit.CategoryID, Role: credit.Role, Character: credit.Character, Parts: credit.Parts,
			Work: personWorkRow(credit.DiscographyRow),
		}
		rows[i].Work.PosterURL = posters[credit.ID]
	}
	return view, rows, nil
}

// KnownFor maps what the library's media type knows the person for.
func (a People) KnownFor(ctx context.Context, id, categoryID int64, visible []int64) ([]gen.KnownFor, error) {
	if _, err := a.People.Get(ctx, id, visible); errors.Is(err, library.ErrPersonNotFound) {
		return nil, fmt.Errorf("%w: %w", apiserver.ErrPersonNotFound, err)
	} else if err != nil {
		return nil, err
	}
	views := []gen.KnownFor{}
	profiling, ok := a.Profiling(categoryID)
	if !ok {
		return views, nil
	}
	titles, err := profiling.KnownFor(ctx, id, categoryID)
	if err != nil {
		return nil, err
	}
	for _, title := range titles {
		view := gen.KnownFor{Provider: title.Provider, ExternalId: title.ExternalID, Kind: title.Kind, Title: title.Title}
		if title.Year > 0 {
			view.Year = &title.Year
		}
		if title.WorkID > 0 {
			view.WorkId = &title.WorkID
		}
		setString(&view.PosterUrl, title.PosterURL)
		setString(&view.Role, title.Role)
		views = append(views, view)
	}
	return views, nil
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
	if work.Subtype.Valid {
		row.PrimaryType = &work.Subtype.String
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
