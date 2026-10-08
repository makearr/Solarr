package apiserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
)

// The subjects a follow can be of.
const (
	SubjectPerson       = "person"
	SubjectOrganisation = "organisation"
)

// ErrNotFollowable reports a library whose media type has nothing a follow could add: 400.
var ErrNotFollowable = errors.New("apiserver: this library cannot follow anyone")

// ErrFollowSourceMissing reports a follow whose provider is not set up (no TMDB key, no artist id yet): 409.
var ErrFollowSourceMissing = errors.New("apiserver: the provider a follow needs is not available")

// Follower follows people and organisations into libraries. Every call answers a subject no library in visible
// knows of with ErrPersonNotFound or ErrOrganisationNotFound.
type Follower interface {
	Follows(ctx context.Context, subjectType string, subjectID, userID int64, visible []int64) ([]gen.Follow, error)
	Follow(ctx context.Context, subjectType string, subjectID, userID int64, visible []int64, request gen.FollowRequest) (gen.Follow, error)
	Unfollow(ctx context.Context, subjectType string, subjectID, userID, categoryID int64, visible []int64) error
	Preview(ctx context.Context, subjectType string, subjectID int64, visible []int64, request gen.FollowRequest) (gen.FollowPreview, error)
}

// followCaller answers the subject id, the caller and what they may read; false means the answer was written.
func (s *Server) followCaller(w http.ResponseWriter, r *http.Request) (int64, authz.Access, []int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "id must be a number")
		return 0, authz.Access{}, nil, false
	}
	if s.follower == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "following is not available")
		return 0, authz.Access{}, nil, false
	}
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return 0, authz.Access{}, nil, false
	}
	visible, ok := s.personVisibility(w, r)
	return id, access, visible, ok
}

// followBody reads a follow request and checks the caller may add to its library; false means the answer was written.
func (s *Server) followBody(w http.ResponseWriter, r *http.Request, access authz.Access) (gen.FollowRequest, bool) {
	var body gen.FollowRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "the request body could not be read")
		return gen.FollowRequest{}, false
	}
	if _, err := access.Category(body.CategoryId, settings.PermissionRequest); err != nil {
		writeError(w, s.logger, err)
		return gen.FollowRequest{}, false
	}
	return body, true
}

// writeFollowError maps the follow errors; anything else is writeError's.
func (s *Server) writeFollowError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrPersonNotFound), errors.Is(err, ErrOrganisationNotFound):
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such person or organisation")
	case errors.Is(err, ErrNotFollowable), errors.Is(err, ErrInvalidChoice):
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", err.Error())
	case errors.Is(err, ErrFollowSourceMissing):
		writeProblem(w, s.logger, http.StatusConflict, TypeConflict, "Cannot follow yet", err.Error())
	default:
		writeError(w, s.logger, err)
	}
}

func (s *Server) listFollows(w http.ResponseWriter, r *http.Request, subjectType string) {
	id, access, visible, ok := s.followCaller(w, r)
	if !ok {
		return
	}
	follows, err := s.follower.Follows(r.Context(), subjectType, id, access.UserID(), visible)
	if err != nil {
		s.writeFollowError(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, follows)
}

func (s *Server) putFollow(w http.ResponseWriter, r *http.Request, subjectType string) {
	id, access, visible, ok := s.followCaller(w, r)
	if !ok {
		return
	}
	body, ok := s.followBody(w, r, access)
	if !ok {
		return
	}
	follow, err := s.follower.Follow(r.Context(), subjectType, id, access.UserID(), visible, body)
	if err != nil {
		s.writeFollowError(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, follow)
}

func (s *Server) deleteFollow(w http.ResponseWriter, r *http.Request, subjectType string) {
	id, access, visible, ok := s.followCaller(w, r)
	if !ok {
		return
	}
	categoryID, err := strconv.ParseInt(r.URL.Query().Get("category_id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "category_id must be a number")
		return
	}
	if _, err := access.Category(categoryID, settings.PermissionRequest); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if err := s.follower.Unfollow(r.Context(), subjectType, id, access.UserID(), categoryID, visible); err != nil {
		s.writeFollowError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) previewFollow(w http.ResponseWriter, r *http.Request, subjectType string) {
	id, access, visible, ok := s.followCaller(w, r)
	if !ok {
		return
	}
	body, ok := s.followBody(w, r, access)
	if !ok {
		return
	}
	preview, err := s.follower.Preview(r.Context(), subjectType, id, visible, body)
	if err != nil {
		s.writeFollowError(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, preview)
}

// ListPersonFollows answers the caller's follows of a person, one per library.
func (s *Server) ListPersonFollows(w http.ResponseWriter, r *http.Request, _ int64) {
	s.listFollows(w, r, SubjectPerson)
}

// FollowPerson follows a person into one library.
func (s *Server) FollowPerson(w http.ResponseWriter, r *http.Request, _ int64) {
	s.putFollow(w, r, SubjectPerson)
}

// UnfollowPerson ends the caller's follow of a person in one library.
func (s *Server) UnfollowPerson(w http.ResponseWriter, r *http.Request, _ int64, _ gen.UnfollowPersonParams) {
	s.deleteFollow(w, r, SubjectPerson)
}

// PreviewPersonFollow answers what following a person would add.
func (s *Server) PreviewPersonFollow(w http.ResponseWriter, r *http.Request, _ int64) {
	s.previewFollow(w, r, SubjectPerson)
}

// ListOrganisationFollows answers the caller's follows of an organisation, one per library.
func (s *Server) ListOrganisationFollows(w http.ResponseWriter, r *http.Request, _ int64) {
	s.listFollows(w, r, SubjectOrganisation)
}

// FollowOrganisation follows an organisation into one library.
func (s *Server) FollowOrganisation(w http.ResponseWriter, r *http.Request, _ int64) {
	s.putFollow(w, r, SubjectOrganisation)
}

// UnfollowOrganisation ends the caller's follow of an organisation in one library.
func (s *Server) UnfollowOrganisation(w http.ResponseWriter, r *http.Request, _ int64, _ gen.UnfollowOrganisationParams) {
	s.deleteFollow(w, r, SubjectOrganisation)
}

// PreviewOrganisationFollow answers what following an organisation would add.
func (s *Server) PreviewOrganisationFollow(w http.ResponseWriter, r *http.Request, _ int64) {
	s.previewFollow(w, r, SubjectOrganisation)
}

// mountFollows registers the follow routes of both subjects on the secured router.
func (s *Server) mountFollows(router chi.Router) {
	for base, subject := range map[string]string{"/people": SubjectPerson, "/organisations": SubjectOrganisation} {
		router.Get(base+"/{id}/follow", func(w http.ResponseWriter, r *http.Request) { s.listFollows(w, r, subject) })
		router.Put(base+"/{id}/follow", func(w http.ResponseWriter, r *http.Request) { s.putFollow(w, r, subject) })
		router.Delete(base+"/{id}/follow", func(w http.ResponseWriter, r *http.Request) { s.deleteFollow(w, r, subject) })
		router.Post(base+"/{id}/follow/preview", func(w http.ResponseWriter, r *http.Request) { s.previewFollow(w, r, subject) })
	}
}
