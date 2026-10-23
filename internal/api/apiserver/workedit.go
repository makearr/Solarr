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

// ErrTagNameTaken marks a rename onto a name another tag has; the answer is 409.
var ErrTagNameTaken = errors.New("apiserver: tag name taken")

// ErrTagMove marks a tag put under another taxonomy's or its own subtree; the answer is 409.
var ErrTagMove = errors.New("apiserver: a tag moves only within its taxonomy, never under itself")

// TagRow is one tag of the tree; ParentID 0 is the top of its taxonomy, Taxonomy "" a free label.
type TagRow struct {
	ID       int64
	Name     string
	ParentID int64
	Taxonomy string
	Source   string
	Works    int64
	Aliases  []string
}

// WorkEditRow is an album's Edit dialog: its monitoring, its own quality profile (nil is the library's) and the
// tags a person assigned.
type WorkEditRow struct {
	Monitored bool
	ProfileID *int64
	Tags      []string
	// Fields are the type's editable facets the user set, by facet key; "" returns one to its sources.
	Fields map[string]string
	// RootFolderIDs are the roots holding the work's files; read only.
	RootFolderIDs []int64
}

// WorkEditor reads and writes the Edit dialog. Defined here and implemented by the caller.
type WorkEditor interface {
	// GetWorkEdit answers a missing work id with an error wrapping sql.ErrNoRows.
	GetWorkEdit(ctx context.Context, workID int64) (WorkEditRow, error)
	// SaveWorkEdit answers a missing work id with an error wrapping sql.ErrNoRows and an unknown profile with
	// ErrInvalidWorkEdit.
	SaveWorkEdit(ctx context.Context, workID int64, edit WorkEditRow) error
	// TagTree answers the tags of a taxonomy, "*" for every one and the free labels.
	TagTree(ctx context.Context, taxonomy string) ([]TagRow, error)
	// CreateTag answers a taken name with ErrTagNameTaken, a parent of another taxonomy with ErrTagMove.
	CreateTag(ctx context.Context, name, taxonomy string, parentID int64) (TagRow, error)
	// EditTag renames and places a tag; a missing id wraps sql.ErrNoRows.
	EditTag(ctx context.Context, id int64, name string, parentID int64) error
	// MergeTag folds one tag into another of its taxonomy.
	MergeTag(ctx context.Context, fromID, intoID int64) error
	// DeleteTag removes the tag from every work; a missing id wraps sql.ErrNoRows.
	DeleteTag(ctx context.Context, id int64) error
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
	// Fields are editable facets to set; MonitorOption, with Specials, is one of the type's options applied last.
	Fields        map[string]string
	MonitorOption string
	Specials      bool
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
	roots := row.RootFolderIDs
	if roots == nil {
		roots = []int64{}
	}
	view := gen.WorkEdit{Monitored: row.Monitored, ProfileId: row.ProfileID, Tags: row.Tags, RootFolderIds: &roots}
	if len(row.Fields) > 0 {
		view.Fields = &row.Fields
	}
	writeJSON(w, s.logger, http.StatusOK, view)
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
	edit := WorkEditRow{Monitored: body.Monitored, ProfileID: body.ProfileId, Tags: body.Tags, Fields: derefOr(body.Fields, nil)}
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
	case errors.Is(err, ErrTagNameTaken):
		writeProblem(w, s.logger, http.StatusConflict, TypeConflict, "Conflict", "another tag already has that name")
	case errors.Is(err, ErrTagMove):
		writeProblem(w, s.logger, http.StatusConflict, TypeConflict, "Conflict", "a tag moves only within its taxonomy, never under itself")
	case errors.Is(err, sql.ErrNoRows):
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such work")
	default:
		writeError(w, s.logger, err)
	}
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
		Where: derefOr(body.Where, ""),
	}
	if selection.TagIDs, selection.ExcludedTagIDs, err = parseTagChips(derefOr(body.Tags, "")); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "tags must be tag ids, a negative one to leave out")
		return
	}
	change := BulkEditChange{
		Monitored: body.Monitored, AddTags: derefOr(body.AddTags, nil), RemoveTags: derefOr(body.RemoveTags, nil),
		Fields: derefOr(body.Fields, nil), MonitorOption: derefOr(body.MonitorOption, ""), Specials: derefOr(body.Specials, false),
	}
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
