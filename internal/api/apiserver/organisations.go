package apiserver

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/platform/paginate"
)

// ErrOrganisationNotFound reports an organisation no library the caller may read knows of: 404, never 403.
var ErrOrganisationNotFound = errors.New("apiserver: organisation not found")

// OrganisationsReader lists and reads the labels, networks, studios and publishers of every media type.
type OrganisationsReader interface {
	ListOrganisations(ctx context.Context, query PeopleQuery, after paginate.TextCursor, limit int) ([]gen.OrganisationEntry, error)
	// GetOrganisation answers one no library in visible knows of with ErrOrganisationNotFound.
	GetOrganisation(ctx context.Context, id int64, visible []int64) (gen.OrganisationDetail, []PersonCreditRow, error)
}

// routeListOrganisations parses the query string into the generated parameters.
func (s *Server) routeListOrganisations(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	var params gen.ListOrganisationsParams
	if raw := query.Get("category"); raw != "" {
		category, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "category must be a number")
			return
		}
		params.Category = &category
	}
	if raw := query.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "limit must be a number")
			return
		}
		params.Limit = &limit
	}
	if raw := query.Get("monitored"); raw != "" {
		monitored, err := strconv.ParseBool(raw)
		if err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "monitored must be true or false")
			return
		}
		params.Monitored = &monitored
	}
	for name, target := range map[string]**string{"cursor": &params.Cursor, "from": &params.From, "q": &params.Q, "role": &params.Role} {
		if raw := query.Get(name); raw != "" {
			*target = &raw
		}
	}
	if raw := query.Get("sort"); raw != "" {
		sort := gen.ListOrganisationsParamsSort(raw)
		params.Sort = &sort
	}
	if raw := query.Get("dir"); raw != "" {
		dir := gen.ListOrganisationsParamsDir(raw)
		params.Dir = &dir
	}
	s.ListOrganisations(w, r, params)
}

// ListOrganisations answers a page of organisations of one library, or of every library the caller may read.
func (s *Server) ListOrganisations(w http.ResponseWriter, r *http.Request, params gen.ListOrganisationsParams) {
	if s.organisations == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "organisations are not available")
		return
	}
	cursor, err := paginate.DecodeText(derefOr(params.Cursor, ""))
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", err.Error())
		return
	}
	limit := paginate.ClampLimit(derefOr(params.Limit, 0))
	visible, ok := s.personVisibility(w, r)
	if !ok {
		return
	}
	query := PeopleQuery{
		Visible: visible, Category: derefOr(params.Category, 0), Monitored: params.Monitored, NameContains: derefOr(params.Q, ""),
		Sort: string(derefOr(params.Sort, gen.ListOrganisationsParamsSortName)),
		Desc: derefOr(params.Dir, gen.ListOrganisationsParamsDirAsc) == gen.ListOrganisationsParamsDirDesc,
	}
	for _, role := range strings.Split(derefOr(params.Role, ""), ",") {
		if role = strings.TrimSpace(role); role != "" {
			query.Roles = append(query.Roles, role)
		}
	}
	if query.Category != 0 && !slices.Contains(visible, query.Category) {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such library")
		return
	}
	if cursor.Sort == "" && cursor.ID == 0 && params.From != nil && !query.Desc && query.Sort == string(gen.ListOrganisationsParamsSortName) {
		cursor = paginate.TextCursor{Sort: *params.From}
	}
	rows, err := s.organisations.ListOrganisations(r.Context(), query, cursor, limit+1)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	page := gen.OrganisationPage{Items: rows}
	if len(rows) > limit {
		page.Items = rows[:limit]
		last := page.Items[limit-1]
		sortValue := last.SortName
		switch query.Sort {
		case string(gen.ListOrganisationsParamsSortWorks):
			sortValue = strconv.FormatInt(last.Works, 10)
		case string(gen.ListOrganisationsParamsSortLatest):
			sortValue = strconv.Itoa(derefOr(last.Latest, 0))
		}
		next := paginate.TextCursor{Sort: sortValue, ID: last.Id}.Encode()
		page.NextCursor = &next
	}
	writeJSON(w, s.logger, http.StatusOK, page)
}

// routeGetOrganisation parses the id path parameter.
func (s *Server) routeGetOrganisation(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "an organisation id must be a number")
		return
	}
	s.GetOrganisation(w, r, id)
}

// GetOrganisation answers one organisation with its works in every library the caller may read.
func (s *Server) GetOrganisation(w http.ResponseWriter, r *http.Request, id int64) {
	if s.organisations == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "organisations are not available")
		return
	}
	visible, ok := s.personVisibility(w, r)
	if !ok {
		return
	}
	detail, credits, err := s.organisations.GetOrganisation(r.Context(), id, visible)
	if errors.Is(err, ErrOrganisationNotFound) {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such organisation")
		return
	}
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	detail.Credits = creditViews(credits, s.wall.Now())
	writeJSON(w, s.logger, http.StatusOK, detail)
}
