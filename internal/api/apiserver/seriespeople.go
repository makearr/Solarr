package apiserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/paginate"
)

// SeriesPeopleCatalog is the TV people and network screens' data, scoped to one library the caller was checked for.
type SeriesPeopleCatalog interface {
	// People returns up to limit+1 people after the cursor, by name.
	People(ctx context.Context, categoryID int64, roles []string, prefix string, after paginate.TextCursor, limit int) ([]gen.SeriesPerson, error)
	Person(ctx context.Context, categoryID, personID, userID int64) (gen.SeriesPersonDetail, error)
	Networks(ctx context.Context, categoryID int64) ([]gen.SeriesNetwork, error)
	Network(ctx context.Context, categoryID, networkID, userID int64) (gen.SeriesNetworkDetail, error)
	// Follow subscribes the user to a "person" or an "organisation" so the library adds their new series itself.
	Follow(ctx context.Context, userID, categoryID int64, subject string, subjectID int64, request gen.SeriesFollowRequest) (gen.SeriesFollow, error)
	Unfollow(ctx context.Context, userID int64, subject string, subjectID int64) error
}

// ErrFollowUnavailable means the subject cannot be followed (no TMDB id to follow by, or it is followed for another
// media type); answered as 409 with the reason.
var ErrFollowUnavailable = errors.New("apiserver: this cannot be followed")

// peopleAccessAs is peopleAccess for a caller who needs more than reading, and answers who they are.
func (s *Server) peopleAccessAs(w http.ResponseWriter, r *http.Request, categoryID int64, need settings.Permission) (int64, bool) {
	access, ok := s.seriesAccess(w, r, categoryID, need)
	if !ok {
		return 0, false
	}
	if s.seriesPeople == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "series people are not available")
		return 0, false
	}
	return access.UserID(), true
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
	for name, target := range map[string]**string{"role": &params.Role, "q": &params.Q, "cursor": &params.Cursor} {
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
	rows, err := s.seriesPeople.People(r.Context(), params.CategoryId, roles, strings.TrimSpace(derefOr(params.Q, "")), cursor, limit+1)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	page := gen.SeriesPeoplePage{Items: rows}
	if len(rows) > limit {
		page.Items = rows[:limit]
		last := page.Items[len(page.Items)-1]
		next := paginate.TextCursor{Sort: strings.ToLower(last.Name), ID: last.Id}.Encode()
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
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	detail, err := s.seriesPeople.Person(r.Context(), params.CategoryId, id, access.UserID())
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
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	detail, err := s.seriesPeople.Network(r.Context(), params.CategoryId, id, access.UserID())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, detail)
}

func (s *Server) routeFollowSeriesPerson(w http.ResponseWriter, r *http.Request) {
	s.followSubject(w, r, "person")
}

func (s *Server) routeFollowSeriesNetwork(w http.ResponseWriter, r *http.Request) {
	s.followSubject(w, r, "organisation")
}

// FollowSeriesPerson and FollowSeriesNetwork satisfy the generated interface; the route functions are what is mounted.
func (s *Server) FollowSeriesPerson(w http.ResponseWriter, r *http.Request, _ int64) {
	s.followSubject(w, r, "person")
}

// FollowSeriesNetwork see FollowSeriesPerson.
func (s *Server) FollowSeriesNetwork(w http.ResponseWriter, r *http.Request, _ int64) {
	s.followSubject(w, r, "organisation")
}

func (s *Server) followSubject(w http.ResponseWriter, r *http.Request, subject string) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "id must be a number")
		return
	}
	var body gen.SeriesFollowRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "the request body could not be read")
		return
	}
	userID, ok := s.peopleAccessAs(w, r, body.CategoryId, settings.PermissionManage)
	if !ok {
		return
	}
	follow, err := s.seriesPeople.Follow(r.Context(), userID, body.CategoryId, subject, id, body)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, follow)
}

func (s *Server) routeUnfollowSeriesPerson(w http.ResponseWriter, r *http.Request) {
	s.unfollowSubject(w, r, "person")
}

func (s *Server) routeUnfollowSeriesNetwork(w http.ResponseWriter, r *http.Request) {
	s.unfollowSubject(w, r, "organisation")
}

// UnfollowSeriesPerson and UnfollowSeriesNetwork satisfy the generated interface; the route functions are mounted.
func (s *Server) UnfollowSeriesPerson(w http.ResponseWriter, r *http.Request, _ int64, _ gen.UnfollowSeriesPersonParams) {
	s.unfollowSubject(w, r, "person")
}

// UnfollowSeriesNetwork see UnfollowSeriesPerson.
func (s *Server) UnfollowSeriesNetwork(w http.ResponseWriter, r *http.Request, _ int64, _ gen.UnfollowSeriesNetworkParams) {
	s.unfollowSubject(w, r, "organisation")
}

func (s *Server) unfollowSubject(w http.ResponseWriter, r *http.Request, subject string) {
	id, categoryID, ok := s.idAndCategory(w, r)
	if !ok {
		return
	}
	userID, ok := s.peopleAccessAs(w, r, categoryID, settings.PermissionManage)
	if !ok {
		return
	}
	if err := s.seriesPeople.Unfollow(r.Context(), userID, subject, id); err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
