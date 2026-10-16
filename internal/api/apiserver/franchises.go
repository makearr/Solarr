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

// Franchises are the groups a provider puts works in (a movie's collection), read and followed through the library's
// media type.
type Franchises interface {
	List(ctx context.Context, categoryID int64) ([]gen.Franchise, error)
	// Monitor answers how many works following the franchise added; ErrNoFranchise for an id that is none.
	Monitor(ctx context.Context, categoryID, franchiseID int64, monitored bool) (int, error)
}

// ErrNoFranchise is an id that is no franchise, or a library whose type has none.
var ErrNoFranchise = errors.New("apiserver: no such franchise")

func (s *Server) writeFranchiseError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrNoFranchise) {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such franchise")
		return
	}
	writeError(w, s.logger, err)
}

// ListFranchises answers a library's franchises with the works of each it holds.
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
	if s.franchises == nil {
		writeJSON(w, s.logger, http.StatusOK, struct {
			Franchises []gen.Franchise `json:"franchises"`
		}{[]gen.Franchise{}})
		return
	}
	franchises, err := s.franchises.List(r.Context(), category.ID)
	if err != nil {
		s.writeFranchiseError(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, struct {
		Franchises []gen.Franchise `json:"franchises"`
	}{franchises})
}

// MonitorFranchise follows or stops following a franchise; the person needs to manage the library.
func (s *Server) MonitorFranchise(w http.ResponseWriter, r *http.Request, id int64) {
	var body gen.MonitorFranchiseJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "expected category_id and monitored")
		return
	}
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	category, err := access.Category(body.CategoryId, settings.PermissionManage)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.franchises == nil {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "this library has no franchises")
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

func (s *Server) routeListFranchises(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("category_id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "category_id must be a number")
		return
	}
	s.ListFranchises(w, r, gen.ListFranchisesParams{CategoryId: id})
}

func (s *Server) routeMonitorFranchise(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "an id must be a number")
		return
	}
	s.MonitorFranchise(w, r, id)
}
