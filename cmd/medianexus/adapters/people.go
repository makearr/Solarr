package adapters

import (
	"context"
	"time"

	"github.com/makearr/MediaNexus/internal/api/apiserver"
	"github.com/makearr/MediaNexus/internal/db/library"
)

// People satisfies apiserver.PeopleReader and apiserver.PeopleSubscriber over library.People.
type People struct{ People library.People }

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
		}
		if work.Year.Valid {
			row.Year = &work.Year.Int64
		}
		discography[index] = row
	}

	person := detail.Person
	view := apiserver.PersonDetailRow{
		PersonRow: apiserver.PersonRow{
			ID: person.ID, Name: person.Name, SortName: person.SortName, Monitored: person.Monitored != 0,
		},
		Discography: discography,
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
	if len(detail.Aliases) > 0 {
		view.Aliases = detail.Aliases
	}
	return view, nil
}

func (a People) Subscribe(
	ctx context.Context, personID, userID int64, mediaTypes, roles []string, monitorBackCatalogue bool,
) error {
	_, err := a.People.Subscribe(ctx, time.Now(), personID, userID, library.SubscriptionRequest{
		MediaTypes: mediaTypes, Roles: roles, MonitorBackCatalogue: monitorBackCatalogue,
	})
	return err
}
