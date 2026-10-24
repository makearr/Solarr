package apiserver

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
)

// LibraryMover moves or copies works to another library (plan 10-libraries L3). Defined here, implemented by the caller.
type LibraryMover interface {
	// MoveWorkToLibrary answers the plan, or a refusal when the work cannot go to that library at all.
	MoveWorkToLibrary(ctx context.Context, request LibraryMoveRequest) (DrainPlanResult, string, error)
	// MoveSelectionToLibrary is MoveWorkToLibrary over a selection.
	MoveSelectionToLibrary(ctx context.Context, selection BulkSelection, request LibraryMoveRequest) (BulkLibraryMoveResult, error)
}

// LibraryMoveRequest is one move or copy: the work (0 in a bulk request), where it goes, and whether it is a copy.
type LibraryMoveRequest struct {
	WorkID, ToCategoryID, FromCategoryID int64
	Copy, Preview                        bool
}

// BulkLibraryMoveResult is what a selection's move comes to.
type BulkLibraryMoveResult struct {
	Works, Files, Bytes, Started int64
	Refused                      []LibraryMoveRefusal
}

// LibraryMoveRefusal is one work of a selection that cannot go, and why.
type LibraryMoveRefusal struct {
	WorkID int64
	Title  string
	Reason string
}

func (s *Server) routeMoveWorkToLibrary(w http.ResponseWriter, r *http.Request) {
	if id, ok := s.workIDParam(w, r); ok {
		s.MoveWorkToLibrary(w, r, id)
	}
}

// MoveWorkToLibrary plans or starts one work's move or copy to another library; it moves files, so administrator only.
func (s *Server) MoveWorkToLibrary(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) || !s.libraryMoverAvailable(w) {
		return
	}
	var body gen.MoveWorkToLibraryJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxDrainBody)).Decode(&body); err != nil || body.CategoryId == 0 {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "name the library it goes to")
		return
	}
	result, refusal, err := s.libraryMover.MoveWorkToLibrary(r.Context(), LibraryMoveRequest{
		WorkID: id, ToCategoryID: body.CategoryId, FromCategoryID: derefOr(body.FromCategoryId, 0),
		Copy: derefOr(body.Copy, false), Preview: derefOr(body.Preview, false),
	})
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	view := gen.LibraryMovePlan{Plan: drainPlanView(result)}
	if refusal != "" {
		view.Refusal = &refusal
	}
	writeJSON(w, s.logger, http.StatusOK, view)
}

// BulkMoveWorksToLibrary is MoveWorkToLibrary over a selection.
func (s *Server) BulkMoveWorksToLibrary(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) || !s.libraryMoverAvailable(w) {
		return
	}
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	var body gen.BulkMoveWorksToLibraryJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil || body.CategoryId == 0 {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "name the library they go to")
		return
	}
	rootFolderIDs, categoryIDs, err := s.libraryVisibility(access, body.LibraryId, derefOr(body.WholeLibrary, false), settings.PermissionRead)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	selection := BulkSelection{
		RootFolderIDs: rootFolderIDs, CategoryIDs: categoryIDs, IDs: derefOr(body.Ids, nil), ExcludedIDs: derefOr(body.ExcludedIds, nil),
		Fresh: derefOr(body.Fresh, false), Wanted: derefOr(body.Wanted, false), CutoffUnmet: derefOr(body.CutoffUnmet, false),
		Where: derefOr(body.Where, ""), AboveRating: derefOr(body.AboveRating, false), MediaType: derefOr(body.MediaType, ""),
	}
	if selection.TagIDs, selection.ExcludedTagIDs, err = parseTagChips(derefOr(body.Tags, "")); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "tags must be tag ids, a negative one to leave out")
		return
	}
	result, err := s.libraryMover.MoveSelectionToLibrary(r.Context(), selection, LibraryMoveRequest{
		ToCategoryID: body.CategoryId, FromCategoryID: derefOr(body.LibraryId, 0),
		Copy: derefOr(body.Copy, false), Preview: derefOr(body.Preview, false),
	})
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	view := gen.BulkLibraryMove{Works: result.Works, Files: result.Files, Bytes: result.Bytes, Started: result.Started}
	view.Refused = make([]gen.LibraryMoveRefusal, len(result.Refused))
	for i, refusal := range result.Refused {
		view.Refused[i] = gen.LibraryMoveRefusal{WorkId: refusal.WorkID, Title: refusal.Title, Reason: refusal.Reason}
	}
	writeJSON(w, s.logger, http.StatusOK, view)
}

func (s *Server) libraryMoverAvailable(w http.ResponseWriter) bool {
	if s.libraryMover == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "moving between libraries is not available")
		return false
	}
	return true
}
