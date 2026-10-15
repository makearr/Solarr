package apiserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
)

// WorkNumbering numbers a work by another of its sources' lists (a series by TheTVDB's DVD order).
type WorkNumbering interface {
	// Choices answers the lists and the current one; ErrNoNumbering when the work has no other.
	Choices(ctx context.Context, workID int64) (choices []MediaChoice, current string, err error)
	// Renumber previews or applies numbering by basis; ErrInvalidChoice for a basis the work lacks.
	Renumber(ctx context.Context, workID int64, basis string, preview bool) (gen.NumberingPlan, error)
}

// ErrNoNumbering is a work whose type, or whose sources, offer no other numbering.
var ErrNoNumbering = errors.New("apiserver: the work has no other numbering")

// GetWorkNumbering answers the lists a work can be numbered by.
func (s *Server) GetWorkNumbering(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.workNumbering == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	choices, current, err := s.workNumbering.Choices(r.Context(), id)
	if errors.Is(err, ErrNoNumbering) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	type choice struct {
		Key   string `json:"key"`
		Label string `json:"label"`
	}
	views := make([]choice, len(choices))
	for i, one := range choices {
		views[i] = choice(one)
	}
	writeJSON(w, s.logger, http.StatusOK, struct {
		Current string   `json:"current"`
		Choices []choice `json:"choices"`
	}{current, views})
}

// SetWorkNumbering previews or applies another numbering. Administrator only.
func (s *Server) SetWorkNumbering(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.workNumbering == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "renumbering is not available")
		return
	}
	var body gen.SetWorkNumberingJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body); err != nil || body.Basis == "" {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "expected a basis")
		return
	}
	preview := body.Preview != nil && *body.Preview
	plan, err := s.workNumbering.Renumber(r.Context(), id, body.Basis, preview)
	if errors.Is(err, ErrNoNumbering) {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", err.Error())
		return
	}
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if !preview {
		s.logger.InfoContext(r.Context(), "work renumbered", "work_id", id, "basis", body.Basis, "moved", len(plan.Moves))
	}
	writeJSON(w, s.logger, http.StatusOK, plan)
}
