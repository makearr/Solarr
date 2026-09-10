package apiserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/MediaNexus/internal/api/apiserver/gen"
	"github.com/makearr/MediaNexus/internal/api/authz"
	"github.com/makearr/MediaNexus/internal/db/settings"
	"github.com/makearr/MediaNexus/internal/platform/config"
)

// ListExclusionPatterns returns every ADR-53 part 1 pattern. Unlike works or
// categories, patterns carry no per-category visibility of their own -- an
// authenticated caller sees the whole list, the same as root folders.
func (s *Server) ListExclusionPatterns(w http.ResponseWriter, r *http.Request) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}

	patterns := s.auth.Settings.Snapshot().ExclusionPatterns()
	views := make([]gen.ExclusionPattern, len(patterns))
	for i, p := range patterns {
		views[i] = exclusionPatternView(p)
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}

// CreateExclusionPattern adds a pattern. Administrator only, matching every
// other settings-writing endpoint (CreateCategory, AddRootFolder).
func (s *Server) CreateExclusionPattern(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}

	var body gen.CreateExclusionPatternJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "the request body could not be read")
		return
	}

	created, err := s.auth.Settings.CreateExclusionPattern(r.Context(), exclusionPatternFromWrite(body))
	if err != nil {
		var problems config.Problems
		if errors.As(err, &problems) {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"The pattern has problems", problems.Report("the pattern", "Nothing was created."))
			return
		}
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusCreated, exclusionPatternView(created))
}

// routeUpdateExclusionPattern parses the id path parameter, mirroring routeGetWork.
func (s *Server) routeUpdateExclusionPattern(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "an id must be a number")
		return
	}
	s.UpdateExclusionPattern(w, r, id)
}

// UpdateExclusionPattern replaces a pattern's content. Administrator only.
// Builtin never changes -- settings.Cache's own UpdateExclusionPattern does
// not accept it as a field to change, so there is nothing here that could
// turn a shipped default into a user-added pattern or back.
func (s *Server) UpdateExclusionPattern(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}

	var body gen.UpdateExclusionPatternJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "the request body could not be read")
		return
	}

	if err := s.auth.Settings.UpdateExclusionPattern(r.Context(), id, exclusionPatternFromWrite(body)); err != nil {
		var problems config.Problems
		if errors.As(err, &problems) {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"The pattern has problems", problems.Report("the pattern", "Nothing was changed."))
			return
		}
		if errors.Is(err, settings.ErrNotFound) {
			writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such pattern")
			return
		}
		writeError(w, s.logger, err)
		return
	}

	updated, ok := s.auth.Settings.Snapshot().ExclusionPattern(id)
	if !ok {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such pattern")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, exclusionPatternView(updated))
}

// routeDeleteExclusionPattern parses the id path parameter, mirroring routeDeleteCategory.
func (s *Server) routeDeleteExclusionPattern(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "an id must be a number")
		return
	}
	s.DeleteExclusionPattern(w, r, id)
}

// DeleteExclusionPattern removes a pattern, builtin or not (ADR-53's own
// "editable and removable" text). Administrator only.
func (s *Server) DeleteExclusionPattern(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if err := s.auth.Settings.DeleteExclusionPattern(r.Context(), id); err != nil {
		if errors.Is(err, settings.ErrNotFound) {
			writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such pattern")
			return
		}
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func exclusionPatternView(p settings.ExclusionPattern) gen.ExclusionPattern {
	view := gen.ExclusionPattern{
		Id: p.ID, Pattern: p.Pattern, Action: gen.ExclusionPatternAction(p.Action),
		Note: p.Note, Builtin: p.Builtin, Enabled: p.Enabled,
	}
	if p.MediaType != "" {
		view.MediaType = &p.MediaType
	}
	view.CategoryId = p.CategoryID
	return view
}

func exclusionPatternFromWrite(body gen.ExclusionPatternWrite) settings.ExclusionPattern {
	pattern := settings.ExclusionPattern{
		Pattern: body.Pattern, Action: string(body.Action),
		CategoryID: body.CategoryId, Enabled: true,
	}
	if body.MediaType != nil {
		pattern.MediaType = *body.MediaType
	}
	if body.Note != nil {
		pattern.Note = *body.Note
	}
	if body.Enabled != nil {
		pattern.Enabled = *body.Enabled
	}
	return pattern
}
