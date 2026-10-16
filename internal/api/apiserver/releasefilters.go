package apiserver

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
)

// WorkReleaseFilters holds a series' own rules for the releases offered for it.
type WorkReleaseFilters interface {
	// List answers a work's rules; ErrSeriesNotFound for a work that is not a series.
	List(ctx context.Context, workID int64) ([]gen.ReleaseFilter, error)
	// Replace makes the rules the work's whole set; ErrInvalidChoice names the first rule that cannot be kept.
	Replace(ctx context.Context, workID int64, filters []gen.ReleaseFilter) error
}

// GetWorkReleaseFilters answers a series' rules. Administrator only.
func (s *Server) GetWorkReleaseFilters(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.workReleaseFilters == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "release filters are not available")
		return
	}
	filters, err := s.workReleaseFilters.List(r.Context(), id)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, filters)
}

// SetWorkReleaseFilters replaces a series' rules. Administrator only.
func (s *Server) SetWorkReleaseFilters(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.workReleaseFilters == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "release filters are not available")
		return
	}
	var body gen.SetWorkReleaseFiltersJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "expected a list of rules")
		return
	}
	if err := s.workReleaseFilters.Replace(r.Context(), id, body); err != nil {
		writeError(w, s.logger, err)
		return
	}
	filters, err := s.workReleaseFilters.List(r.Context(), id)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, filters)
}
