package adapters

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/platform/paginate"
)

// Organisations satisfies apiserver.OrganisationsReader over library.Organisations.
type Organisations struct {
	Organisations library.Organisations
	Works         library.Works
}

// ListOrganisations maps one page of the one organisations list.
func (a Organisations) ListOrganisations(ctx context.Context, query apiserver.PeopleQuery, after paginate.TextCursor, limit int) ([]gen.OrganisationEntry, error) {
	cursor := library.PeopleCursor{ID: after.ID, Set: after.Sort != "" || after.ID != 0}
	if query.Sort == library.PeopleByName || query.Sort == "" {
		cursor.Name = after.Sort
	} else {
		cursor.Value, _ = strconv.ParseInt(after.Sort, 10, 64)
	}
	rows, err := a.Organisations.List(ctx, library.OrganisationFilter{
		Visible: query.Visible, Category: query.Category, Roles: query.Roles, Monitored: query.Monitored,
		NameContains: query.NameContains, Sort: query.Sort, Desc: query.Desc,
	}, cursor, limit)
	if err != nil {
		return nil, err
	}
	views := make([]gen.OrganisationEntry, len(rows))
	for i, row := range rows {
		view := gen.OrganisationEntry{
			Id: row.ID, Name: row.Name, SortName: row.SortName, Kind: row.Kind, Monitored: row.Monitored,
			Roles: nonNil(row.Roles), Works: row.Works,
		}
		if row.Latest > 0 {
			latest := int(row.Latest)
			view.Latest = &latest
		}
		setString(&view.Country, row.Country)
		setString(&view.LogoUrl, row.LogoURL)
		views[i] = view
	}
	return views, nil
}

// GetOrganisation maps one organisation and its works in the libraries visible holds.
func (a Organisations) GetOrganisation(ctx context.Context, id int64, visible []int64) (gen.OrganisationDetail, []apiserver.PersonCreditRow, error) {
	profile, err := a.Organisations.Get(ctx, id, visible)
	if errors.Is(err, library.ErrOrganisationNotFound) {
		return gen.OrganisationDetail{}, nil, fmt.Errorf("%w: %w", apiserver.ErrOrganisationNotFound, err)
	}
	if err != nil {
		return gen.OrganisationDetail{}, nil, err
	}
	credits, err := a.Organisations.Credits(ctx, id, visible)
	if err != nil {
		return gen.OrganisationDetail{}, nil, err
	}
	view := gen.OrganisationDetail{
		Id: profile.ID, Name: profile.Name, SortName: profile.SortName, Kind: profile.Kind, Monitored: profile.Monitored,
		Ids: profile.IDs, Libraries: make([]gen.PersonLibrary, len(profile.Libraries)),
	}
	for i, place := range profile.Libraries {
		view.Libraries[i] = gen.PersonLibrary{CategoryId: place.CategoryID, Role: place.Role, Works: place.Works}
	}
	setString(&view.Country, profile.Country)
	setString(&view.LogoUrl, profile.LogoURL)
	ids := make([]int64, len(credits))
	for i, credit := range credits {
		ids[i] = credit.ID
	}
	posters, err := a.Works.Posters(ctx, ids)
	if err != nil {
		return gen.OrganisationDetail{}, nil, err
	}
	rows := make([]apiserver.PersonCreditRow, len(credits))
	for i, credit := range credits {
		rows[i] = apiserver.PersonCreditRow{CategoryID: credit.CategoryID, Role: credit.Role, Work: personWorkRow(credit.DiscographyRow)}
		rows[i].Work.PosterURL = posters[credit.ID]
	}
	return view, rows, nil
}
