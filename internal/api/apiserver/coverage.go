package apiserver

import (
	"context"
	"net/http"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
)

// WorkCoverage edits which parts of other works a work's own parts stand for.
type WorkCoverage interface {
	// Get answers the work's parts and what they cover; ErrSeriesNotFound for a work of a type without coverage.
	Get(ctx context.Context, workID int64) (gen.WorkCoverage, error)
	// Replace makes the hand-made rows of one part exactly the given parts and answers how many named parts do not exist.
	Replace(ctx context.Context, workID int64, editionRef string, covered []gen.CoveredPartRef) (skipped int, err error)
}

// GetWorkCoverage answers what a work's parts cover. Administrator only.
func (s *Server) GetWorkCoverage(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.workCoverage == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "coverage is not available")
		return
	}
	view, err := s.workCoverage.Get(r.Context(), id)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, view)
}

// SetWorkCoverage replaces the hand-made coverage of one part. Administrator only.
func (s *Server) SetWorkCoverage(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.workCoverage == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "coverage is not available")
		return
	}
	var body gen.SetWorkCoverageJSONRequestBody
	if !s.decodeBody(w, r, maxSetupBody, &body, "expected an edition part and the parts it covers") {
		return
	}
	skipped, err := s.workCoverage.Replace(r.Context(), id, body.EditionRef, body.Covered)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	view, err := s.workCoverage.Get(r.Context(), id)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	view.Skipped = skipped
	writeJSON(w, s.logger, http.StatusOK, view)
}
