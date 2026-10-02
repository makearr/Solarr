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

// ErrInvalidWorkEdit marks an edit that names something that does not exist; the answer is 400.
var ErrInvalidWorkEdit = errors.New("apiserver: invalid work edit")

// WorkEditRow is an album's Edit dialog: its monitoring, its own quality profile (nil is the library's) and the
// tags a person assigned.
type WorkEditRow struct {
	Monitored bool
	ProfileID *int64
	Tags      []string
}

// WorkEditor reads and writes the Edit dialog. Defined here and implemented by the caller.
type WorkEditor interface {
	// GetWorkEdit answers a missing work id with an error wrapping sql.ErrNoRows.
	GetWorkEdit(ctx context.Context, workID int64) (WorkEditRow, error)
	// SaveWorkEdit answers a missing work id with an error wrapping sql.ErrNoRows and an unknown profile with
	// ErrInvalidWorkEdit.
	SaveWorkEdit(ctx context.Context, workID int64, edit WorkEditRow) error
	ListTags(ctx context.Context) ([]string, error)
	// BulkEdit applies change to every work the selection matches and answers how many matched; preview changes nothing.
	BulkEdit(ctx context.Context, selection BulkSelection, change BulkEditChange, preview bool) (int64, error)
}

// BulkEditChange is what a bulk edit changes: nil Monitored, false SetProfile and empty tag lists leave a thing alone.
type BulkEditChange struct {
	Monitored  *bool
	SetProfile bool
	ProfileID  *int64
	AddTags    []string
	RemoveTags []string
}

func (s *Server) routeGetWorkEdit(w http.ResponseWriter, r *http.Request) {
	if id, ok := s.workIDParam(w, r); ok {
		s.GetWorkEdit(w, r, id)
	}
}

func (s *Server) routeSaveWorkEdit(w http.ResponseWriter, r *http.Request) {
	if id, ok := s.workIDParam(w, r); ok {
		s.SaveWorkEdit(w, r, id)
	}
}

func (s *Server) workIDParam(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "a work id must be a number")
		return 0, false
	}
	return id, true
}

// GetWorkEdit answers what an album's Edit dialog opens with.
func (s *Server) GetWorkEdit(w http.ResponseWriter, r *http.Request, id int64) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.workDetail == nil || s.workEditor == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "editing is not available")
		return
	}
	if !s.workPermitted(w, r, access, id, settings.PermissionRead) {
		return
	}
	row, err := s.workEditor.GetWorkEdit(r.Context(), id)
	if err != nil {
		s.writeWorkEditError(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, gen.WorkEdit{Monitored: row.Monitored, ProfileId: row.ProfileID, Tags: row.Tags})
}

// SaveWorkEdit applies an album's Edit dialog in one call.
func (s *Server) SaveWorkEdit(w http.ResponseWriter, r *http.Request, id int64) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.workDetail == nil || s.workEditor == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "editing is not available")
		return
	}
	var body gen.SaveWorkEditJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "the request body could not be read")
		return
	}
	if !s.workPermitted(w, r, access, id, settings.PermissionRequest) {
		return
	}
	edit := WorkEditRow{Monitored: body.Monitored, ProfileID: body.ProfileId, Tags: body.Tags}
	if err := s.workEditor.SaveWorkEdit(r.Context(), id, edit); err != nil {
		s.writeWorkEditError(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, body)
}

func (s *Server) writeWorkEditError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalidWorkEdit):
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", err.Error())
	case errors.Is(err, sql.ErrNoRows):
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such work")
	default:
		writeError(w, s.logger, err)
	}
}

// ListTags answers every tag name for the tag picker.
func (s *Server) ListTags(w http.ResponseWriter, r *http.Request) {
	if s.workEditor == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "tags are not available")
		return
	}
	names, err := s.workEditor.ListTags(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, names)
}

// BulkEditWorks is the Edit dialog over a selection; it needs the same permission as monitoring one album.
func (s *Server) BulkEditWorks(w http.ResponseWriter, r *http.Request) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.workEditor == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "editing is not available")
		return
	}
	var body gen.BulkEditWorksJSONBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "the request body could not be read")
		return
	}
	rootFolderIDs, categoryIDs, err := s.visibilityAt(access, body.CategoryId, settings.PermissionRequest)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	selection := BulkSelection{
		RootFolderIDs: rootFolderIDs, CategoryIDs: categoryIDs, IDs: derefOr(body.Ids, nil), ExcludedIDs: derefOr(body.ExcludedIds, nil),
		Fresh: derefOr(body.Fresh, false), Wanted: derefOr(body.Wanted, false), CutoffUnmet: derefOr(body.CutoffUnmet, false),
	}
	change := BulkEditChange{Monitored: body.Monitored, AddTags: derefOr(body.AddTags, nil), RemoveTags: derefOr(body.RemoveTags, nil)}
	if body.Profile != nil {
		change.SetProfile, change.ProfileID = true, body.Profile.Id
	}
	count, err := s.workEditor.BulkEdit(r.Context(), selection, change, derefOr(body.Preview, false))
	if err != nil {
		s.writeWorkEditError(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, gen.BulkResult{Count: count})
}
