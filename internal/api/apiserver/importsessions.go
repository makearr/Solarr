package apiserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/MediaNexus/internal/api/apiserver/gen"
	"github.com/makearr/MediaNexus/internal/api/authz"
)

// ImportSessionLister reads ADR-48 import sessions and their units. Defined
// here and implemented by the caller (cmd/medianexus), the same reasoning
// WorksReader's own doc comment gives for every library-backed interface --
// apiserver never imports internal/db/library directly.
type ImportSessionLister interface {
	ListImportSessions(ctx context.Context, filterState string) ([]ImportSessionRow, error)
	GetImportSession(ctx context.Context, id int64) (ImportSessionRow, error)
	ListImportUnits(ctx context.Context, sessionID int64, filterDecision string, afterID int64, limit int) ([]ImportUnitRow, error)
	// CancelImportSession only flips the flag the job itself polls for (ADR-48).
	CancelImportSession(ctx context.Context, id int64) error
}

// ErrImportSessionNotRunning is CancelImportSession's refusal to a non-running session.
var ErrImportSessionNotRunning = errors.New("apiserver: import session is not running")

// ImportInspector enqueues ADR-48's own inspection job. Its own interface,
// separate from ImportSessionLister, since a real caller might reasonably
// want reads without the write (or the reverse).
type ImportInspector interface {
	InspectImport(ctx context.Context, sourcePath, origin string, categoryID *int64) error
}

// ImportSessionRow is one import_session row.
type ImportSessionRow struct {
	ID                                      int64
	SourcePath, Origin                      string
	CategoryID                              *int64
	State                                   string
	Threshold                               float64
	UnitsTotal, UnitsCommitted, UnitsQueued int64
	UnitsSkipped, UnitsFailed               int64
	BytesImported                           int64
	StartedAt                               time.Time
	FinishedAt                              *time.Time
	Error                                   string
}

// ImportEvidenceSignal is one GROUP signal that shaped a unit's boundary.
type ImportEvidenceSignal struct{ Kind, Detail string }

// ImportEvidence is every signal recorded for one unit (ADR-10 shape).
type ImportEvidence struct {
	Signals   []ImportEvidenceSignal
	Ambiguous bool
}

// ImportCandidate is one IDENTIFY strategy's ranked answer for a unit.
type ImportCandidate struct {
	Strategy             string
	WorkID               *int64
	Provider, ExternalID string
	Confidence           float64
}

// ImportUnitRow is one import_unit row.
type ImportUnitRow struct {
	ID, SessionID     int64
	RelPath, Shape    string
	FileCount, Bytes  int64
	Confidence        float64
	Decision          string
	WorkID, EditionID *int64
	Evidence          ImportEvidence
	Candidates        []ImportCandidate
	Reason            string
}

// InspectImport enqueues ADR-48's own inspection job. Administrator only --
// it points a real filesystem walk at whatever path the caller names, the
// same trust boundary a root folder's own path already carries.
func (s *Server) InspectImport(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.importInspector == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "import inspection is not available")
		return
	}

	var body gen.InspectImportJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "the request body could not be read")
		return
	}
	if body.SourcePath == "" {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "source_path is required")
		return
	}
	origin := "manual"
	if body.Origin != nil {
		origin = string(*body.Origin)
	}

	if err := s.importInspector.InspectImport(r.Context(), body.SourcePath, origin, body.CategoryId); err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusAccepted, gen.ImportInspectResult{Enqueued: true})
}

// routeListImportSessions parses the state query parameter, mirroring
// routeGetWork's own reasoning for path parameters.
func (s *Server) routeListImportSessions(w http.ResponseWriter, r *http.Request) {
	var params gen.ListImportSessionsParams
	if raw := r.URL.Query().Get("state"); raw != "" {
		state := gen.ListImportSessionsParamsState(raw)
		params.State = &state
	}
	s.ListImportSessions(w, r, params)
}

// routeGetImportSession parses the id path parameter, mirroring routeGetWork.
func (s *Server) routeGetImportSession(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "a session id must be a number")
		return
	}
	s.GetImportSession(w, r, id)
}

// routeCancelImportSession parses the id path parameter, mirroring routeGetImportSession.
func (s *Server) routeCancelImportSession(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "a session id must be a number")
		return
	}
	s.CancelImportSession(w, r, id)
}

// routeListImportUnits parses the id path parameter and the decision/
// after_id/limit query parameters, mirroring routeListBlockedContent.
func (s *Server) routeListImportUnits(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "a session id must be a number")
		return
	}

	query := r.URL.Query()
	var params gen.ListImportUnitsParams
	if raw := query.Get("decision"); raw != "" {
		decision := gen.ListImportUnitsParamsDecision(raw)
		params.Decision = &decision
	}
	if raw := query.Get("after_id"); raw != "" {
		afterID, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"Bad request", "after_id must be a number")
			return
		}
		params.AfterId = &afterID
	}
	if raw := query.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"Bad request", "limit must be a number")
			return
		}
		params.Limit = &limit
	}
	s.ListImportUnits(w, r, id, params)
}

// ListImportSessions returns every session, newest first, optionally
// filtered to one state. Any authenticated caller, the same as root folders
// and exclusion patterns -- a session carries no per-category visibility of
// its own (category_id is nullable, "decide per unit").
func (s *Server) ListImportSessions(w http.ResponseWriter, r *http.Request, params gen.ListImportSessionsParams) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.importSessions == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "import sessions are not available")
		return
	}

	filterState := ""
	if params.State != nil {
		filterState = string(*params.State)
	}
	rows, err := s.importSessions.ListImportSessions(r.Context(), filterState)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	views := make([]gen.ImportSession, len(rows))
	for i, row := range rows {
		views[i] = importSessionView(row)
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}

// GetImportSession answers one session by id.
func (s *Server) GetImportSession(w http.ResponseWriter, r *http.Request, id int64) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.importSessions == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "import sessions are not available")
		return
	}

	row, err := s.importSessions.GetImportSession(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such import session")
		return
	}
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, importSessionView(row))
}

// CancelImportSession asks a running session to stop. Administrator only,
// the same trust boundary InspectImport itself already carries.
func (s *Server) CancelImportSession(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.importSessions == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "import sessions are not available")
		return
	}

	err := s.importSessions.CancelImportSession(r.Context(), id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such import session")
	case errors.Is(err, ErrImportSessionNotRunning):
		writeProblem(w, s.logger, http.StatusConflict, TypeConflict, "Not running", "this session is not currently running")
	case err != nil:
		writeError(w, s.logger, err)
	default:
		w.WriteHeader(http.StatusAccepted)
	}
}

// ListImportUnits returns one page of a session's units, cursor-paginated by
// id (the same plain ascending cursor /blocked already uses).
func (s *Server) ListImportUnits(w http.ResponseWriter, r *http.Request, id int64, params gen.ListImportUnitsParams) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.importSessions == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "import sessions are not available")
		return
	}

	if _, err := s.importSessions.GetImportSession(r.Context(), id); errors.Is(err, sql.ErrNoRows) {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such import session")
		return
	} else if err != nil {
		writeError(w, s.logger, err)
		return
	}

	filterDecision := ""
	if params.Decision != nil {
		filterDecision = string(*params.Decision)
	}
	afterID := int64(0)
	if params.AfterId != nil {
		afterID = *params.AfterId
	}
	limit := 50
	if params.Limit != nil {
		limit = *params.Limit
	}

	rows, err := s.importSessions.ListImportUnits(r.Context(), id, filterDecision, afterID, limit)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	views := make([]gen.ImportUnit, len(rows))
	for i, row := range rows {
		views[i] = importUnitView(row)
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}

func importSessionView(row ImportSessionRow) gen.ImportSession {
	view := gen.ImportSession{
		Id: row.ID, SourcePath: row.SourcePath, Origin: gen.ImportSessionOrigin(row.Origin),
		CategoryId: row.CategoryID, State: gen.ImportSessionState(row.State),
		Threshold: float32(row.Threshold), UnitsTotal: row.UnitsTotal,
		UnitsCommitted: row.UnitsCommitted, UnitsQueued: row.UnitsQueued,
		UnitsSkipped: row.UnitsSkipped, UnitsFailed: row.UnitsFailed,
		BytesImported: row.BytesImported, StartedAt: row.StartedAt,
	}
	if row.FinishedAt != nil {
		view.FinishedAt = row.FinishedAt
	}
	if row.Error != "" {
		view.Error = &row.Error
	}
	return view
}

func importUnitView(row ImportUnitRow) gen.ImportUnit {
	signals := make([]gen.ImportEvidenceSignal, len(row.Evidence.Signals))
	for i, s := range row.Evidence.Signals {
		view := gen.ImportEvidenceSignal{Kind: s.Kind}
		if s.Detail != "" {
			view.Detail = &s.Detail
		}
		signals[i] = view
	}
	candidates := make([]gen.ImportCandidate, len(row.Candidates))
	for i, c := range row.Candidates {
		view := gen.ImportCandidate{Strategy: c.Strategy, Confidence: float32(c.Confidence)}
		if c.WorkID != nil {
			view.WorkId = c.WorkID
		}
		if c.Provider != "" {
			view.Provider = &c.Provider
		}
		if c.ExternalID != "" {
			view.ExternalId = &c.ExternalID
		}
		candidates[i] = view
	}

	view := gen.ImportUnit{
		Id: row.ID, SessionId: row.SessionID, RelPath: row.RelPath,
		Shape: gen.ImportUnitShape(row.Shape), FileCount: row.FileCount, Bytes: row.Bytes,
		Confidence: float32(row.Confidence), Decision: gen.ImportUnitDecision(row.Decision),
		Evidence:   &gen.ImportEvidence{Signals: &signals, Ambiguous: &row.Evidence.Ambiguous},
		Candidates: &candidates,
	}
	if row.WorkID != nil {
		view.WorkId = row.WorkID
	}
	if row.EditionID != nil {
		view.EditionId = row.EditionID
	}
	if row.Reason != "" {
		view.Reason = &row.Reason
	}
	return view
}
