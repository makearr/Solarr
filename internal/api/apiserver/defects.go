package apiserver

import (
	"context"
	"net/http"
	"strconv"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
)

// DefectRow is one broken or incomplete file of a library.
type DefectRow struct {
	MediaObjectID, RootFolderID, WorkID, SizeBytes, DetectedAt int64
	RelPath, WorkTitle, Reason, Detail                         string
}

// DefectOutcome is what resolving defective files did.
type DefectOutcome struct {
	Resolved int
	WorkIDs  []int64
	Problems []string
}

// FileDefects lists the defective files of a library's roots and recycles them.
type FileDefects interface {
	ListDefects(ctx context.Context, rootFolderIDs []int64, afterID int64, limit int) ([]DefectRow, error)
	CountDefects(ctx context.Context, rootFolderIDs []int64) (int64, error)
	// ResolveDefects recycles the files named (those on the roots only) and reopens what they satisfied.
	ResolveDefects(ctx context.Context, rootFolderIDs, mediaObjectIDs []int64) (DefectOutcome, error)
}

const defectPageLimit = 100

// ListCategoryDefects answers a page of a library's defective files.
func (s *Server) ListCategoryDefects(w http.ResponseWriter, r *http.Request, id int64, params gen.ListCategoryDefectsParams) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if _, err := access.Category(id, settings.PermissionRead); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.fileDefects == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "defective files are not available")
		return
	}
	var after int64
	if params.Cursor != nil && *params.Cursor != "" {
		parsed, err := strconv.ParseInt(*params.Cursor, 10, 64)
		if err != nil || parsed < 0 {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "cursor is not one this server gave")
			return
		}
		after = parsed
	}
	limit := min(max(derefOr(params.Limit, defectPageLimit), 1), 200)
	roots := s.categoryRootIDs(id)
	rows, err := s.fileDefects.ListDefects(r.Context(), roots, after, limit+1)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	total, err := s.fileDefects.CountDefects(r.Context(), roots)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	page := gen.DefectPage{Total: total, Items: []gen.DefectItem{}}
	if len(rows) > limit {
		rows = rows[:limit]
		next := strconv.FormatInt(rows[len(rows)-1].MediaObjectID, 10)
		page.NextCursor = &next
	}
	for _, row := range rows {
		page.Items = append(page.Items, gen.DefectItem{
			MediaObjectId: row.MediaObjectID, WorkId: row.WorkID, WorkTitle: row.WorkTitle, Path: row.RelPath, SizeBytes: row.SizeBytes,
			Reason: gen.DefectItemReason(row.Reason), Detail: row.Detail, DetectedAt: row.DetectedAt,
		})
	}
	writeJSON(w, s.logger, http.StatusOK, page)
}

// ResolveCategoryDefects recycles defective files of the library and reopens what they held; manage access.
func (s *Server) ResolveCategoryDefects(w http.ResponseWriter, r *http.Request, id int64) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if _, err := access.Category(id, settings.PermissionManage); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.fileDefects == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "defective files are not available")
		return
	}
	var body gen.ResolveCategoryDefectsJSONRequestBody
	if !s.decodeBody(w, r, maxImportBody, &body, "an action and the files are required") {
		return
	}
	if len(body.Ids) == 0 {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "at least one file is required")
		return
	}
	outcome, err := s.fileDefects.ResolveDefects(r.Context(), s.categoryRootIDs(id), body.Ids)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	resolution := gen.DefectResolution{Resolved: outcome.Resolved, WorkIds: []int64{}, Problems: nonNil(outcome.Problems)}
	if body.Action == gen.DefectResolveRequestActionRerequest && outcome.WorkIDs != nil {
		resolution.WorkIds = outcome.WorkIDs
	}
	writeJSON(w, s.logger, http.StatusOK, resolution)
}

// categoryRootIDs are the root folders of a library.
func (s *Server) categoryRootIDs(categoryID int64) []int64 {
	folders := s.auth.Settings.Snapshot().RootFoldersForCategory(categoryID)
	ids := make([]int64, len(folders))
	for i, folder := range folders {
		ids[i] = folder.ID
	}
	return ids
}
