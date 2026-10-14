package apiserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
)

// WorkMerger folds one work into another of its type (an album held twice).
type WorkMerger interface {
	// Suggestion answers the work id was found to be; false when none.
	Suggestion(ctx context.Context, id int64) (gen.WorkMergeSuggestion, bool, error)
	// Merge folds dropID into keepID, or with preview only says what would move. ErrMergeRefused for a work into itself
	// or into another type; sql.ErrNoRows for a missing work.
	Merge(ctx context.Context, dropID, keepID int64, preview bool) (gen.WorkMerge, error)
}

// ErrMergeRefused is a merge the two works cannot take.
var ErrMergeRefused = errors.New("apiserver: a work merges only into another work of its type")

func (s *Server) routeWorkID(w http.ResponseWriter, r *http.Request, handle func(http.ResponseWriter, *http.Request, int64)) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "a work id must be a number")
		return
	}
	handle(w, r, id)
}

// GetWorkMergeSuggestion answers the work this one was found to be. Administrator only.
func (s *Server) GetWorkMergeSuggestion(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.workMerger == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	suggestion, found, err := s.workMerger.Suggestion(r.Context(), id)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if !found {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, suggestion)
}

// MergeWork folds a work into another of its type, or previews it. Administrator only.
func (s *Server) MergeWork(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.workMerger == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "merging is not available")
		return
	}
	var body gen.MergeWorkJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "expected the work to merge into")
		return
	}
	preview := body.Preview != nil && *body.Preview
	merged, err := s.workMerger.Merge(r.Context(), id, body.Into, preview)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such work")
	case errors.Is(err, ErrMergeRefused):
		writeProblem(w, s.logger, http.StatusConflict, TypeConflict, "Cannot merge", err.Error())
	case err != nil:
		writeError(w, s.logger, err)
	default:
		if !preview {
			s.logger.InfoContext(r.Context(), "work merged", "work_id", id, "into", body.Into, "files", merged.Files)
		}
		writeJSON(w, s.logger, http.StatusOK, merged)
	}
}
