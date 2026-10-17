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
)

// Franchises are a library's collections: the groups a provider puts works in (a movie's collection, a series with its
// spin-offs) and the ones people make by hand.
type Franchises interface {
	List(ctx context.Context, categoryID int64) ([]gen.Franchise, error)
	// Get answers one collection with every work of it, those the library lacks included.
	Get(ctx context.Context, categoryID, franchiseID int64) (gen.Franchise, error)
	// Create makes an empty collection by hand.
	Create(ctx context.Context, categoryID int64, name string) (gen.Franchise, error)
	// Rename, Delete, AddMember and RemoveMember answer ErrFranchiseNotEditable for a provider's collection.
	Rename(ctx context.Context, categoryID, franchiseID int64, name string) error
	Delete(ctx context.Context, categoryID, franchiseID int64) error
	AddMember(ctx context.Context, categoryID, franchiseID, workID int64) error
	RemoveMember(ctx context.Context, categoryID, franchiseID, workID int64) error
	// Monitor answers how many works following the franchise added; ErrNoFranchise for an id that is none.
	Monitor(ctx context.Context, categoryID, franchiseID int64, monitored bool) (int, error)
}

// ErrNoFranchise is an id that is no franchise, or a library whose type has none.
var ErrNoFranchise = errors.New("apiserver: no such franchise")

// ErrFranchiseNotEditable is a collection a provider names: only hand-made ones are renamed, deleted or filled by hand.
var ErrFranchiseNotEditable = errors.New("apiserver: a provider's collection cannot be changed by hand")

func (s *Server) writeFranchiseError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNoFranchise):
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such collection")
	case errors.Is(err, ErrFranchiseNotEditable):
		writeProblem(w, s.logger, http.StatusConflict, TypeConflict, "Conflict", "only a collection made by hand can be changed")
	default:
		writeError(w, s.logger, err)
	}
}

// franchiseCategory finds the library for a read (read permission) or a change (manage).
func (s *Server) franchiseCategory(w http.ResponseWriter, r *http.Request, categoryID int64, permission settings.Permission) (settings.Category, bool) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return settings.Category{}, false
	}
	category, err := access.Category(categoryID, permission)
	if err != nil {
		writeError(w, s.logger, err)
		return settings.Category{}, false
	}
	if s.franchises == nil {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "this library has no collections")
		return settings.Category{}, false
	}
	return category, true
}

// ListFranchises answers a library's collections with the works of each it holds.
func (s *Server) ListFranchises(w http.ResponseWriter, r *http.Request, params gen.ListFranchisesParams) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	category, err := access.Category(params.CategoryId, settings.PermissionRead)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	franchises := []gen.Franchise{}
	if s.franchises != nil {
		if franchises, err = s.franchises.List(r.Context(), category.ID); err != nil {
			s.writeFranchiseError(w, err)
			return
		}
	}
	writeJSON(w, s.logger, http.StatusOK, struct {
		Franchises []gen.Franchise `json:"franchises"`
	}{franchises})
}

// GetFranchise answers one collection with every work of it, those the library lacks included.
func (s *Server) GetFranchise(w http.ResponseWriter, r *http.Request, id int64, params gen.GetFranchiseParams) {
	category, ok := s.franchiseCategory(w, r, params.CategoryId, settings.PermissionRead)
	if !ok {
		return
	}
	franchise, err := s.franchises.Get(r.Context(), category.ID, id)
	if err != nil {
		s.writeFranchiseError(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, franchise)
}

// CreateFranchise makes an empty collection by hand.
func (s *Server) CreateFranchise(w http.ResponseWriter, r *http.Request) {
	var body gen.CreateFranchiseJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body); err != nil || strings.TrimSpace(body.Name) == "" {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "expected category_id and a name")
		return
	}
	category, ok := s.franchiseCategory(w, r, body.CategoryId, settings.PermissionManage)
	if !ok {
		return
	}
	franchise, err := s.franchises.Create(r.Context(), category.ID, strings.TrimSpace(body.Name))
	if err != nil {
		s.writeFranchiseError(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusCreated, franchise)
}

// RenameFranchise renames a collection made by hand.
func (s *Server) RenameFranchise(w http.ResponseWriter, r *http.Request, id int64, params gen.RenameFranchiseParams) {
	var body gen.RenameFranchiseJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body); err != nil || strings.TrimSpace(body.Name) == "" {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "expected a name")
		return
	}
	category, ok := s.franchiseCategory(w, r, params.CategoryId, settings.PermissionManage)
	if !ok {
		return
	}
	if err := s.franchises.Rename(r.Context(), category.ID, id, strings.TrimSpace(body.Name)); err != nil {
		s.writeFranchiseError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DeleteFranchise deletes a collection made by hand; the works in it stay.
func (s *Server) DeleteFranchise(w http.ResponseWriter, r *http.Request, id int64, params gen.DeleteFranchiseParams) {
	category, ok := s.franchiseCategory(w, r, params.CategoryId, settings.PermissionManage)
	if !ok {
		return
	}
	if err := s.franchises.Delete(r.Context(), category.ID, id); err != nil {
		s.writeFranchiseError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// AddFranchiseMember puts a work in a collection made by hand.
func (s *Server) AddFranchiseMember(w http.ResponseWriter, r *http.Request, id, workID int64, params gen.AddFranchiseMemberParams) {
	category, ok := s.franchiseCategory(w, r, params.CategoryId, settings.PermissionManage)
	if !ok {
		return
	}
	if err := s.franchises.AddMember(r.Context(), category.ID, id, workID); err != nil {
		s.writeFranchiseError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// RemoveFranchiseMember takes a work out of a collection made by hand.
func (s *Server) RemoveFranchiseMember(w http.ResponseWriter, r *http.Request, id, workID int64, params gen.RemoveFranchiseMemberParams) {
	category, ok := s.franchiseCategory(w, r, params.CategoryId, settings.PermissionManage)
	if !ok {
		return
	}
	if err := s.franchises.RemoveMember(r.Context(), category.ID, id, workID); err != nil {
		s.writeFranchiseError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// MonitorFranchise follows or stops following a franchise; the person needs to manage the library.
func (s *Server) MonitorFranchise(w http.ResponseWriter, r *http.Request, id int64) {
	var body gen.MonitorFranchiseJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "expected category_id and monitored")
		return
	}
	category, ok := s.franchiseCategory(w, r, body.CategoryId, settings.PermissionManage)
	if !ok {
		return
	}
	added, err := s.franchises.Monitor(r.Context(), category.ID, id, body.Monitored)
	if err != nil {
		s.writeFranchiseError(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, struct {
		Added int `json:"added"`
	}{added})
}

func parseFranchiseIDs(r *http.Request, names ...string) ([]int64, bool) {
	ids := make([]int64, len(names))
	for index, name := range names {
		id, err := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
		if err != nil {
			return nil, false
		}
		ids[index] = id
	}
	return ids, true
}

func (s *Server) franchiseRoute(handle func(w http.ResponseWriter, r *http.Request, id int64, category int64)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ids, ok := parseFranchiseIDs(r, "id")
		category, err := strconv.ParseInt(r.URL.Query().Get("category_id"), 10, 64)
		if !ok || err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "the id and category_id must be numbers")
			return
		}
		handle(w, r, ids[0], category)
	}
}

func (s *Server) routeListFranchises(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("category_id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "category_id must be a number")
		return
	}
	s.ListFranchises(w, r, gen.ListFranchisesParams{CategoryId: id})
}

func (s *Server) routeGetFranchise(w http.ResponseWriter, r *http.Request) {
	s.franchiseRoute(func(w http.ResponseWriter, r *http.Request, id, category int64) {
		s.GetFranchise(w, r, id, gen.GetFranchiseParams{CategoryId: category})
	})(w, r)
}

func (s *Server) routeRenameFranchise(w http.ResponseWriter, r *http.Request) {
	s.franchiseRoute(func(w http.ResponseWriter, r *http.Request, id, category int64) {
		s.RenameFranchise(w, r, id, gen.RenameFranchiseParams{CategoryId: category})
	})(w, r)
}

func (s *Server) routeDeleteFranchise(w http.ResponseWriter, r *http.Request) {
	s.franchiseRoute(func(w http.ResponseWriter, r *http.Request, id, category int64) {
		s.DeleteFranchise(w, r, id, gen.DeleteFranchiseParams{CategoryId: category})
	})(w, r)
}

func (s *Server) routeFranchiseMember(add bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ids, ok := parseFranchiseIDs(r, "id", "workId")
		category, err := strconv.ParseInt(r.URL.Query().Get("category_id"), 10, 64)
		if !ok || err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "the ids and category_id must be numbers")
			return
		}
		if add {
			s.AddFranchiseMember(w, r, ids[0], ids[1], gen.AddFranchiseMemberParams{CategoryId: category})
		} else {
			s.RemoveFranchiseMember(w, r, ids[0], ids[1], gen.RemoveFranchiseMemberParams{CategoryId: category})
		}
	}
}

func (s *Server) routeMonitorFranchise(w http.ResponseWriter, r *http.Request) {
	ids, ok := parseFranchiseIDs(r, "id")
	if !ok {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "an id must be a number")
		return
	}
	s.MonitorFranchise(w, r, ids[0])
}
