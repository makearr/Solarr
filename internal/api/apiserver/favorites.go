package apiserver

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
)

// Favorites keeps each user's hearts; a heart acquires nothing, unlike a follow. Defined here, implemented by the caller.
type Favorites interface {
	Set(ctx context.Context, userID int64, subjectType string, subjectID int64, favorite bool) error
	IDs(ctx context.Context, userID int64, subjectType string) ([]int64, error)
}

var favoriteSubjects = map[gen.FavoriteSubject]bool{
	gen.FavoriteSubjectWork: true, gen.FavoriteSubjectWorkPart: true, gen.FavoriteSubjectPerson: true,
	gen.FavoriteSubjectOrganisation: true, gen.FavoriteSubjectCollection: true,
}

// favoriteCaller answers the caller's user id; false means the answer was written.
func (s *Server) favoriteCaller(w http.ResponseWriter, r *http.Request, subjectType gen.FavoriteSubject) (int64, bool) {
	if s.favorites == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "favorites are not available")
		return 0, false
	}
	if !favoriteSubjects[subjectType] {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "this kind of thing cannot be a favorite")
		return 0, false
	}
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return 0, false
	}
	return access.UserID(), true
}

// favoriteViewer is the user id a favorites-only filter narrows to: the caller's when the list asked for it, else 0.
func favoriteViewer(only *bool, access authz.Access) int64 {
	if derefOr(only, false) {
		return access.UserID()
	}
	return 0
}

// ListFavorites answers the ids of one kind of thing the caller has hearted.
func (s *Server) ListFavorites(w http.ResponseWriter, r *http.Request, params gen.ListFavoritesParams) {
	userID, ok := s.favoriteCaller(w, r, params.SubjectType)
	if !ok {
		return
	}
	ids, err := s.favorites.IDs(r.Context(), userID, string(params.SubjectType))
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, ids)
}

func (s *Server) setFavorite(w http.ResponseWriter, r *http.Request, subjectType gen.FavoriteSubject, id int64, favorite bool) {
	userID, ok := s.favoriteCaller(w, r, subjectType)
	if !ok {
		return
	}
	if err := s.favorites.Set(r.Context(), userID, string(subjectType), id, favorite); err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// AddFavorite hearts something for the caller.
func (s *Server) AddFavorite(w http.ResponseWriter, r *http.Request, subjectType gen.FavoriteSubject, id int64) {
	s.setFavorite(w, r, subjectType, id, true)
}

// RemoveFavorite takes the caller's heart back.
func (s *Server) RemoveFavorite(w http.ResponseWriter, r *http.Request, subjectType gen.FavoriteSubject, id int64) {
	s.setFavorite(w, r, subjectType, id, false)
}

func (s *Server) routeListFavorites(w http.ResponseWriter, r *http.Request) {
	s.ListFavorites(w, r, gen.ListFavoritesParams{SubjectType: gen.FavoriteSubject(r.URL.Query().Get("subject_type"))})
}

// routeFavorite parses the path, mirroring the other direct routes.
func (s *Server) routeFavorite(favorite bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := parsePathID(w, s, r, "id", "an id must be a number")
		if err != nil {
			return
		}
		s.setFavorite(w, r, gen.FavoriteSubject(chi.URLParam(r, "subjectType")), id, favorite)
	}
}
