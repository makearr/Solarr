package apiserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/MediaNexus/internal/apiserver/gen"
	"github.com/makearr/MediaNexus/internal/authz"
)

// maxDrainBody bounds what a caller can make the server read for a drain request.
const maxDrainBody = 4 << 10

// ErrNothingToDrain means the root folder has no files on it -- not a
// server error, so writeError maps it to 409 rather than 500.
var ErrNothingToDrain = errors.New("root folder has no files to drain")

// DrainStarter starts or stops ADR-51's own drain over a root folder.
// Defined here and implemented by the caller (cmd/medianexus), the same
// reasoning every library-backed interface in this package already gives --
// apiserver never imports internal/library or internal/mover directly.
type DrainStarter interface {
	StartDrain(ctx context.Context, rootFolderID int64, preview bool) (DrainPlanResult, error)
	StopDrain(ctx context.Context, rootFolderID int64) error
}

// MoveJobLister reads ADR-51 move jobs.
type MoveJobLister interface {
	ListMoveJobs(ctx context.Context, filterReason, filterState string) ([]MoveJobRow, error)
}

// RenameStarter applies section 11.8's own naming template to a root's
// already-identified files. Same DrainPlanResult shape DrainStarter
// answers -- structurally identical (accepted/moves/refused/deltas), not
// drain-specific despite the name, and not worth a second type until the
// two shapes actually need to diverge.
type RenameStarter interface {
	StartRename(ctx context.Context, rootFolderID int64, preview bool) (DrainPlanResult, error)
}

// DrainPlanResult is what one drain computation answered, in apiserver's own
// plain-data shape -- no mover.MovePlan or library types cross this
// boundary.
type DrainPlanResult struct {
	Accepted   bool
	MoveJobID  int64
	FilesTotal int64
	BytesTotal int64
	Moves      []DrainMoveRow
	Refused    []DrainRefusalRow
	RootDeltas []RootDeltaRow
}

// DrainMoveRow is one file a drain plan approved.
type DrainMoveRow struct {
	MediaObjectID    int64
	DestRootFolderID int64
	DestRelPath      string
	SizeBytes        int64
	AffinityKey      string
}

// DrainRefusalRow is one file (or every file, for a whole-batch refusal) a
// drain plan could not place.
type DrainRefusalRow struct {
	MediaObjectID int64
	AffinityKey   string
	Reason        string
}

// RootDeltaRow is one root's own preview (Invariant 73).
type RootDeltaRow struct {
	RootFolderID       int64
	BytesIn            int64
	BytesOut           int64
	ResultingFreeBytes int64
}

// MoveJobRow is one move_job row.
type MoveJobRow struct {
	ID                    int64
	Reason, State         string
	SourceRootID          *int64
	FilesTotal, FilesDone int64
	BytesTotal, BytesDone int64
	CreatedAt             time.Time
	FinishedAt            *time.Time
	Error                 string
}

// routeDrainRootFolder parses the id path parameter, mirroring
// routeGetImportSession.
func (s *Server) routeDrainRootFolder(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "a root folder id must be a number")
		return
	}
	s.DrainRootFolder(w, r, id)
}

// DrainRootFolder computes ADR-51's drain plan for one root, administrator
// only -- it can force accepts_new = 0 and move real files, the same trust
// boundary InspectImport's own doc comment states for a filesystem-wide
// action.
func (s *Server) DrainRootFolder(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.drainStarter == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "draining is not available")
		return
	}

	var body gen.DrainRootFolderJSONRequestBody
	if r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxDrainBody)).Decode(&body); err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"Bad request", "the request body could not be read")
			return
		}
	}
	preview := body.Preview != nil && *body.Preview

	result, err := s.drainStarter.StartDrain(r.Context(), id, preview)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, drainPlanView(result))
}

// routeRenameRootFolder parses the id path parameter, mirroring
// routeDrainRootFolder.
func (s *Server) routeRenameRootFolder(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "a root folder id must be a number")
		return
	}
	s.RenameRootFolder(w, r, id)
}

// RenameRootFolder computes section 11.8's own rename plan for one root,
// administrator only -- it can move real files on disk, the same trust
// boundary DrainRootFolder's own doc comment states.
func (s *Server) RenameRootFolder(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.renameStarter == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "renaming is not available")
		return
	}

	var body gen.RenameRootFolderJSONRequestBody
	if r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxDrainBody)).Decode(&body); err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"Bad request", "the request body could not be read")
			return
		}
	}
	preview := body.Preview != nil && *body.Preview

	result, err := s.renameStarter.StartRename(r.Context(), id, preview)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, drainPlanView(result))
}

// routeStopDrainingRootFolder parses the id path parameter, mirroring
// routeGetImportSession.
func (s *Server) routeStopDrainingRootFolder(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "a root folder id must be a number")
		return
	}
	s.StopDrainingRootFolder(w, r, id)
}

// StopDrainingRootFolder clears drain_state and restores accepts_new,
// administrator only.
func (s *Server) StopDrainingRootFolder(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.drainStarter == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "draining is not available")
		return
	}
	if err := s.drainStarter.StopDrain(r.Context(), id); err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// routeListMoveJobs parses the reason/state query parameters, mirroring
// routeListImportSessions.
func (s *Server) routeListMoveJobs(w http.ResponseWriter, r *http.Request) {
	var params gen.ListMoveJobsParams
	query := r.URL.Query()
	if raw := query.Get("reason"); raw != "" {
		reason := gen.ListMoveJobsParamsReason(raw)
		params.Reason = &reason
	}
	if raw := query.Get("state"); raw != "" {
		state := gen.ListMoveJobsParamsState(raw)
		params.State = &state
	}
	s.ListMoveJobs(w, r, params)
}

// ListMoveJobs returns every move job, newest first, optionally filtered by
// reason and/or state. Any authenticated caller -- a move job carries no
// per-category visibility of its own, the same reasoning import sessions
// already follow.
func (s *Server) ListMoveJobs(w http.ResponseWriter, r *http.Request, params gen.ListMoveJobsParams) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.moveJobs == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "move jobs are not available")
		return
	}

	filterReason, filterState := "", ""
	if params.Reason != nil {
		filterReason = string(*params.Reason)
	}
	if params.State != nil {
		filterState = string(*params.State)
	}
	rows, err := s.moveJobs.ListMoveJobs(r.Context(), filterReason, filterState)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	views := make([]gen.MoveJob, len(rows))
	for i, row := range rows {
		views[i] = moveJobView(row)
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}

func drainPlanView(result DrainPlanResult) gen.DrainPlan {
	view := gen.DrainPlan{
		Accepted: result.Accepted, FilesTotal: result.FilesTotal, BytesTotal: result.BytesTotal,
		Moves: make([]gen.DrainMove, len(result.Moves)), Refused: make([]gen.DrainRefusal, len(result.Refused)),
		RootDeltas: make([]gen.RootDelta, len(result.RootDeltas)),
	}
	if result.MoveJobID != 0 {
		view.MoveJobId = &result.MoveJobID
	}
	for i, m := range result.Moves {
		view.Moves[i] = gen.DrainMove{
			MediaObjectId: m.MediaObjectID, DestRootFolderId: m.DestRootFolderID,
			DestRelPath: m.DestRelPath, SizeBytes: m.SizeBytes, AffinityKey: m.AffinityKey,
		}
	}
	for i, refusal := range result.Refused {
		view.Refused[i] = gen.DrainRefusal{
			MediaObjectId: refusal.MediaObjectID, AffinityKey: refusal.AffinityKey, Reason: refusal.Reason,
		}
	}
	for i, delta := range result.RootDeltas {
		view.RootDeltas[i] = gen.RootDelta{
			RootFolderId: delta.RootFolderID, BytesIn: delta.BytesIn, BytesOut: delta.BytesOut,
			ResultingFreeBytes: delta.ResultingFreeBytes,
		}
	}
	return view
}

func moveJobView(row MoveJobRow) gen.MoveJob {
	view := gen.MoveJob{
		Id: row.ID, Reason: gen.MoveJobReason(row.Reason), State: gen.MoveJobState(row.State),
		FilesTotal: row.FilesTotal, FilesDone: row.FilesDone,
		BytesTotal: row.BytesTotal, BytesDone: row.BytesDone, CreatedAt: row.CreatedAt,
	}
	if row.SourceRootID != nil {
		view.SourceRootId = row.SourceRootID
	}
	if row.FinishedAt != nil {
		view.FinishedAt = row.FinishedAt
	}
	if row.Error != "" {
		view.Error = &row.Error
	}
	return view
}
