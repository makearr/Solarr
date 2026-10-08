package apiserver

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/paginate"
)

// SeriesPeopleCatalog is the TV people and network screens' data, scoped to one library the caller was checked for.
type SeriesPeopleCatalog interface {
	// People returns up to limit+1 people after the cursor; a malformed custom filter answers ErrInvalidChoice.
	People(ctx context.Context, query SeriesPeopleQuery, after paginate.TextCursor, limit int) ([]gen.SeriesPerson, error)
	Person(ctx context.Context, categoryID, personID int64) (gen.SeriesPersonDetail, error)
	Networks(ctx context.Context, categoryID int64) ([]gen.SeriesNetwork, error)
	Network(ctx context.Context, categoryID, networkID int64) (gen.SeriesNetworkDetail, error)
}

// SeriesPeopleQuery is one page request of the cast list.
type SeriesPeopleQuery struct {
	CategoryID int64
	Roles      []string
	Prefix     string
	Monitored  *bool
	// Sort is "name" or "series"; Desc turns it round.
	Sort string
	Desc bool
	// Where is the custom filter as the JSON the request carried.
	Where string
}

// peopleAccess is seriesAccess for the people endpoints, which also need their own catalogue wired.
func (s *Server) peopleAccess(w http.ResponseWriter, r *http.Request, categoryID int64) bool {
	if _, ok := s.seriesAccess(w, r, categoryID, settings.PermissionRead); !ok {
		return false
	}
	if s.seriesPeople == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "series people are not available")
		return false
	}
	return true
}

func (s *Server) routeListSeriesPeople(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	categoryID, err := strconv.ParseInt(query.Get("category_id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "category_id is required and must be a number")
		return
	}
	params := gen.ListSeriesPeopleParams{CategoryId: categoryID}
	for name, target := range map[string]**string{"role": &params.Role, "q": &params.Q, "cursor": &params.Cursor, "where": &params.Where} {
		if raw := query.Get(name); raw != "" {
			*target = &raw
		}
	}
	if raw := query.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "limit must be a number")
			return
		}
		params.Limit = &limit
	}
	if raw := query.Get("monitored"); raw == "true" || raw == "false" {
		monitored := raw == "true"
		params.Monitored = &monitored
	}
	if raw := query.Get("sort"); raw != "" {
		sort := gen.ListSeriesPeopleParamsSort(raw)
		params.Sort = &sort
	}
	if raw := query.Get("dir"); raw != "" {
		dir := gen.ListSeriesPeopleParamsDir(raw)
		params.Dir = &dir
	}
	s.ListSeriesPeople(w, r, params)
}

// ListSeriesPeople answers one page of the people credited on a TV library's series.
func (s *Server) ListSeriesPeople(w http.ResponseWriter, r *http.Request, params gen.ListSeriesPeopleParams) {
	if !s.peopleAccess(w, r, params.CategoryId) {
		return
	}
	cursor, err := paginate.DecodeText(derefOr(params.Cursor, ""))
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", err.Error())
		return
	}
	var roles []string
	for _, role := range strings.Split(derefOr(params.Role, ""), ",") {
		if role = strings.TrimSpace(role); role != "" {
			roles = append(roles, role)
		}
	}
	limit := paginate.ClampLimit(derefOr(params.Limit, 0))
	sort := string(derefOr(params.Sort, "name"))
	rows, err := s.seriesPeople.People(r.Context(), SeriesPeopleQuery{
		CategoryID: params.CategoryId, Roles: roles, Prefix: strings.TrimSpace(derefOr(params.Q, "")), Monitored: params.Monitored,
		Sort: sort, Desc: derefOr(params.Dir, "asc") == "desc", Where: derefOr(params.Where, ""),
	}, cursor, limit+1)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	page := gen.SeriesPeoplePage{Items: rows}
	if len(rows) > limit {
		page.Items = rows[:limit]
		last := page.Items[len(page.Items)-1]
		position := strings.ToLower(last.Name)
		if sort == "series" {
			position = strconv.FormatInt(last.SeriesCount, 10)
		}
		next := paginate.TextCursor{Sort: position, ID: last.Id}.Encode()
		page.NextCursor = &next
	}
	writeJSON(w, s.logger, http.StatusOK, page)
}

// idAndCategory reads the path id and the category of the detail endpoints.
func (s *Server) idAndCategory(w http.ResponseWriter, r *http.Request) (id, categoryID int64, ok bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err == nil {
		categoryID, err = strconv.ParseInt(r.URL.Query().Get("category_id"), 10, 64)
	}
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "id and category_id must be numbers")
		return 0, 0, false
	}
	return id, categoryID, true
}

func (s *Server) routeGetSeriesPerson(w http.ResponseWriter, r *http.Request) {
	if id, category, ok := s.idAndCategory(w, r); ok {
		s.GetSeriesPerson(w, r, id, gen.GetSeriesPersonParams{CategoryId: category})
	}
}

// GetSeriesPerson answers one person's page.
func (s *Server) GetSeriesPerson(w http.ResponseWriter, r *http.Request, id int64, params gen.GetSeriesPersonParams) {
	if !s.peopleAccess(w, r, params.CategoryId) {
		return
	}
	detail, err := s.seriesPeople.Person(r.Context(), params.CategoryId, id)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, detail)
}

func (s *Server) routeListSeriesNetworks(w http.ResponseWriter, r *http.Request) {
	categoryID, err := strconv.ParseInt(r.URL.Query().Get("category_id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "category_id is required and must be a number")
		return
	}
	s.ListSeriesNetworks(w, r, gen.ListSeriesNetworksParams{CategoryId: categoryID})
}

// ListSeriesNetworks answers the networks of a TV library's series.
func (s *Server) ListSeriesNetworks(w http.ResponseWriter, r *http.Request, params gen.ListSeriesNetworksParams) {
	if !s.peopleAccess(w, r, params.CategoryId) {
		return
	}
	networks, err := s.seriesPeople.Networks(r.Context(), params.CategoryId)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, networks)
}

func (s *Server) routeGetSeriesNetwork(w http.ResponseWriter, r *http.Request) {
	if id, category, ok := s.idAndCategory(w, r); ok {
		s.GetSeriesNetwork(w, r, id, gen.GetSeriesNetworkParams{CategoryId: category})
	}
}

// GetSeriesNetwork answers one network with the library's series on it.
func (s *Server) GetSeriesNetwork(w http.ResponseWriter, r *http.Request, id int64, params gen.GetSeriesNetworkParams) {
	if !s.peopleAccess(w, r, params.CategoryId) {
		return
	}
	detail, err := s.seriesPeople.Network(r.Context(), params.CategoryId, id)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, detail)
}
