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
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
)

// Crossovers are events across works and parts listed as one broadcast under two works (Stage 8 T23).
type Crossovers interface {
	List(ctx context.Context, mediaType string, categoryID int64) ([]gen.Crossover, []gen.SameContentSuggestion, error)
	Create(ctx context.Context, mediaType, name string, partIDs []int64) (gen.Crossover, error)
	// Get, Update, Delete and Monitor answer ErrNoCrossover for an id that is none.
	Get(ctx context.Context, id int64) (gen.Crossover, error)
	Update(ctx context.Context, id int64, name string, partIDs []int64) (gen.Crossover, error)
	Delete(ctx context.Context, id int64) error
	Monitor(ctx context.Context, id int64, monitored bool) (gen.Crossover, error)
	ForWork(ctx context.Context, workID int64) ([]gen.Crossover, []gen.SameContentLink, error)
	Link(ctx context.Context, partID, otherPartID int64) error
	Unlink(ctx context.Context, partID, otherPartID int64) error
}

// ErrNoCrossover is an id that is no crossover.
var ErrNoCrossover = errors.New("apiserver: no such crossover")

func (s *Server) crossoversAvailable(w http.ResponseWriter) bool {
	if s.crossovers == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "crossovers are not available")
		return false
	}
	return true
}

func (s *Server) writeCrossoverError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrNoCrossover) {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such crossover")
		return
	}
	writeError(w, s.logger, err)
}

// ListCrossovers answers a library's events and likely one-broadcast pairs.
func (s *Server) ListCrossovers(w http.ResponseWriter, r *http.Request, params gen.ListCrossoversParams) {
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
	if !s.crossoversAvailable(w) {
		return
	}
	events, suggestions, err := s.crossovers.List(r.Context(), category.MediaType, category.ID)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, struct {
		Events      []gen.Crossover             `json:"events"`
		Suggestions []gen.SameContentSuggestion `json:"suggestions"`
	}{events, suggestions})
}

// CreateCrossover records an event. Administrator only.
func (s *Server) CreateCrossover(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) || !s.crossoversAvailable(w) {
		return
	}
	var body gen.CrossoverWrite
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body); err != nil || body.Name == "" || body.CategoryId == nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "expected a name, a library and its episodes")
		return
	}
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	category, err := access.Category(*body.CategoryId, settings.PermissionRead)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	event, err := s.crossovers.Create(r.Context(), category.MediaType, body.Name, body.PartIds)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusCreated, event)
}

// GetCrossover answers one event to a caller who can see one of its series; to anyone else it is no such crossover.
func (s *Server) GetCrossover(w http.ResponseWriter, r *http.Request, id int64) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if !s.crossoversAvailable(w) {
		return
	}
	event, err := s.crossovers.Get(r.Context(), id)
	if err == nil {
		var visible bool
		if visible, err = s.crossoverVisible(r.Context(), access, event); err == nil && !visible {
			err = ErrNoCrossover
		}
	}
	if err != nil {
		s.writeCrossoverError(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, event)
}

// crossoverVisible reports whether the caller can see a work of one of the event's members.
func (s *Server) crossoverVisible(ctx context.Context, access authz.Access, event gen.Crossover) (bool, error) {
	rootFolderIDs, categoryIDs := s.visibleAt(access, settings.PermissionRead)
	for _, member := range event.Members {
		row, err := s.workDetail.GetWorkDetail(ctx, member.WorkId)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return false, err
		}
		if workDetailVisible(rootFolderIDs, categoryIDs, row) {
			return true, nil
		}
	}
	return false, nil
}

// UpdateCrossover renames an event and sets its episodes. Administrator only.
func (s *Server) UpdateCrossover(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) || !s.crossoversAvailable(w) {
		return
	}
	var body gen.CrossoverWrite
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body); err != nil || body.Name == "" {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "expected a name and the episodes")
		return
	}
	event, err := s.crossovers.Update(r.Context(), id, body.Name, body.PartIds)
	if err != nil {
		s.writeCrossoverError(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, event)
}

// DeleteCrossover forgets an event. Administrator only.
func (s *Server) DeleteCrossover(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) || !s.crossoversAvailable(w) {
		return
	}
	if err := s.crossovers.Delete(r.Context(), id); err != nil {
		s.writeCrossoverError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// MonitorCrossover monitors or unmonitors a whole event. Administrator only.
func (s *Server) MonitorCrossover(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) || !s.crossoversAvailable(w) {
		return
	}
	var body gen.MonitorCrossoverJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "expected monitored")
		return
	}
	event, err := s.crossovers.Monitor(r.Context(), id, body.Monitored)
	if err != nil {
		s.writeCrossoverError(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, event)
}

// GetWorkCrossovers answers the events and one-broadcast links of a work's episodes.
func (s *Server) GetWorkCrossovers(w http.ResponseWriter, r *http.Request, id int64) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if !s.workVisible(w, r, access, id) || !s.crossoversAvailable(w) {
		return
	}
	events, links, err := s.crossovers.ForWork(r.Context(), id)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, struct {
		Events      []gen.Crossover       `json:"events"`
		SameContent []gen.SameContentLink `json:"same_content"`
	}{events, links})
}

// LinkSameContent records two episodes as one broadcast. Administrator only.
func (s *Server) LinkSameContent(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) || !s.crossoversAvailable(w) {
		return
	}
	var body gen.LinkSameContentJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body); err != nil || body.PartId == id {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "expected another episode")
		return
	}
	if err := s.crossovers.Link(r.Context(), id, body.PartId); err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// UnlinkSameContent forgets two episodes were one broadcast. Administrator only.
func (s *Server) UnlinkSameContent(w http.ResponseWriter, r *http.Request, id int64, params gen.UnlinkSameContentParams) {
	if !s.requireAdministrator(w, r) || !s.crossoversAvailable(w) {
		return
	}
	if err := s.crossovers.Unlink(r.Context(), id, params.PartId); err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) routeCrossoverID(w http.ResponseWriter, r *http.Request, handle func(http.ResponseWriter, *http.Request, int64)) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "an id must be a number")
		return
	}
	handle(w, r, id)
}

func (s *Server) routeListCrossovers(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("category_id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "category_id must be a number")
		return
	}
	s.ListCrossovers(w, r, gen.ListCrossoversParams{CategoryId: id})
}

func (s *Server) routeUnlinkSameContent(w http.ResponseWriter, r *http.Request) {
	s.routeCrossoverID(w, r, func(w http.ResponseWriter, r *http.Request, id int64) {
		other, err := strconv.ParseInt(r.URL.Query().Get("part_id"), 10, 64)
		if err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "part_id must be a number")
			return
		}
		s.UnlinkSameContent(w, r, id, gen.UnlinkSameContentParams{PartId: other})
	})
}
