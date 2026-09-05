package library

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/internal/store/gen/librarydb"
)

// Import session states (import_session.state, migration 0018).
const (
	ImportInspecting = "INSPECTING"
	ImportImporting  = "IMPORTING"
	ImportDone       = "DONE"
	ImportFailed     = "FAILED"
	ImportReverted   = "REVERTED"
)

// Import session origins (import_session.origin).
const (
	ImportOriginManual      = "manual"
	ImportOriginAcquisition = "acquisition"
	ImportOriginWatched     = "watched"
	ImportOriginAdoption    = "adoption"
)

// Import unit shapes (import_unit.shape).
const (
	UnitSingle    = "SINGLE"
	UnitSet       = "SET"
	UnitPack      = "PACK"
	UnitMultiDisc = "MULTI_DISC"
)

// Import unit decisions (import_unit.decision). A unit is recorded QUEUE by
// default -- IDENTIFY does not exist yet (private/questions.md), so nothing
// commits automatically today; SetUnitDecision is what a later IDENTIFY pass
// calls once it does.
const (
	UnitCommit = "COMMIT"
	UnitQueue  = "QUEUE"
	UnitSkip   = "SKIP"
	UnitFailed = "FAILED"
)

// ImportSessions is ADR-48's own persistence: one row per inspection-and-import
// run, and one row per unit it found. internal/inspect.Session/Unit are pure and
// in-memory (deliberately, ADR-23); this is where a caller persists them, so a
// session survives past one process and a revert has something to undo.
type ImportSessions struct{ store *store.Store }

// NewImportSessions builds it over an open library store.
func NewImportSessions(opened *store.Store) ImportSessions { return ImportSessions{store: opened} }

// Session is one import_session row.
type Session struct {
	ID             int64
	SourcePath     string
	Origin         string
	AcquisitionID  *int64
	CategoryID     *int64
	State          string
	Threshold      float64
	UnitsTotal     int64
	UnitsCommitted int64
	UnitsQueued    int64
	BytesImported  int64
	ExtractDir     string
	StartedAt      int64
	FinishedAt     *int64
	Error          string
}

// NewSession is what Create needs to start one run.
type NewSession struct {
	SourcePath    string
	Origin        string
	AcquisitionID *int64
	CategoryID    *int64
	Threshold     float64
	StartedAt     int64
}

// Unit is one import_unit row -- inspect.Unit's own persisted counterpart, plus
// the columns only IDENTIFY/DECIDE can fill in (WorkID, EditionID, a real
// Decision beyond the QUEUE default, Confidence).
type Unit struct {
	ID             int64
	SessionID      int64
	RelPath        string
	Shape          string
	FileCount      int64
	Bytes          int64
	Confidence     float64
	Decision       string
	WorkID         *int64
	EditionID      *int64
	EvidenceJSON   string
	CandidatesJSON string
	Reason         string
}

// NewUnit is what AddUnits needs for one discovered unit. Confidence, WorkID
// and EditionID are zero/nil and Decision is UnitQueue by default -- exactly
// what WALK/CLASSIFY/GROUP alone can support before IDENTIFY exists.
type NewUnit struct {
	RelPath      string
	Shape        string
	FileCount    int64
	Bytes        int64
	EvidenceJSON string
	Reason       string
}

// Create starts one session in ImportInspecting, with no units yet.
func (s ImportSessions) Create(ctx context.Context, params NewSession) (int64, error) {
	var id int64
	err := s.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		row, err := librarydb.New(tx).CreateImportSession(ctx, librarydb.CreateImportSessionParams{
			SourcePath: params.SourcePath, Origin: params.Origin,
			AcquisitionID: nullInt64(params.AcquisitionID), CategoryID: nullInt64(params.CategoryID),
			Threshold: params.Threshold, StartedAt: params.StartedAt,
		})
		if err != nil {
			return err
		}
		id = row.ID
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("library: create import session: %w", err)
	}
	return id, nil
}

// AddUnits records every unit one inspection pass found, in one transaction
// (Invariant 2), then recomputes the session's own running counts -- the same
// drift-and-repair shape CategoryCounters already uses, rather than trying to
// keep a running total consistent through every future call site that can
// change a unit's decision.
func (s ImportSessions) AddUnits(ctx context.Context, sessionID int64, units []NewUnit) error {
	if len(units) == 0 {
		return nil
	}
	err := s.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		for _, unit := range units {
			if _, err := queries.InsertImportUnit(ctx, librarydb.InsertImportUnitParams{
				SessionID: sessionID, RelPath: unit.RelPath, Shape: unit.Shape,
				FileCount: unit.FileCount, Bytes: unit.Bytes, Confidence: 0,
				Decision: UnitQueue, EvidenceJson: unit.EvidenceJSON, Reason: nullString(unit.Reason),
			}); err != nil {
				return fmt.Errorf("insert unit %s: %w", unit.RelPath, err)
			}
		}
		return queries.RecomputeImportSessionCounts(ctx, sessionID)
	})
	if err != nil {
		return fmt.Errorf("library: add import units to session %d: %w", sessionID, err)
	}
	return nil
}

// SetUnitDecision is the one mutation IDENTIFY needs once it exists: a unit
// starts UnitQueue with no identity, and this fills in a real result without
// this package needing to know how that result was produced.
func (s ImportSessions) SetUnitDecision(
	ctx context.Context, unitID int64, decision string, confidence float64,
	workID, editionID *int64, candidatesJSON, reason string,
) error {
	unit, err := librarydb.New(s.store.Read()).GetImportUnit(ctx, unitID)
	if err != nil {
		return fmt.Errorf("library: set import unit %d decision: %w", unitID, err)
	}
	err = s.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		if err := queries.SetImportUnitDecision(ctx, librarydb.SetImportUnitDecisionParams{
			ID: unitID, Decision: decision, Confidence: confidence,
			WorkID: nullInt64(workID), EditionID: nullInt64(editionID),
			CandidatesJson: nullString(candidatesJSON), Reason: nullString(reason),
		}); err != nil {
			return err
		}
		return queries.RecomputeImportSessionCounts(ctx, unit.SessionID)
	})
	if err != nil {
		return fmt.Errorf("library: set import unit %d decision: %w", unitID, err)
	}
	return nil
}

// Finish transitions a session to a terminal state (ImportDone/ImportFailed/
// ImportReverted) and records when and why.
func (s ImportSessions) Finish(ctx context.Context, sessionID int64, state string, finishedAt int64, errMsg string) error {
	err := s.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).FinishImportSession(ctx, librarydb.FinishImportSessionParams{
			ID: sessionID, State: state, FinishedAt: nullInt64(&finishedAt), Error: nullString(errMsg),
		})
	})
	if err != nil {
		return fmt.Errorf("library: finish import session %d: %w", sessionID, err)
	}
	return nil
}

// SetExtractDir records the workspace a session is using, so a startup sweep
// can find and remove an orphan left behind by a crash (architecture.md §6.1).
func (s ImportSessions) SetExtractDir(ctx context.Context, sessionID int64, dir string) error {
	err := s.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).SetImportSessionExtractDir(ctx, librarydb.SetImportSessionExtractDirParams{
			ID: sessionID, ExtractDir: nullString(dir),
		})
	})
	if err != nil {
		return fmt.Errorf("library: set import session %d extract dir: %w", sessionID, err)
	}
	return nil
}

// Get answers one session by id. A missing id surfaces sql.ErrNoRows, wrapped.
func (s ImportSessions) Get(ctx context.Context, id int64) (Session, error) {
	row, err := librarydb.New(s.store.Read()).GetImportSession(ctx, id)
	if err != nil {
		return Session{}, fmt.Errorf("library: get import session %d: %w", id, err)
	}
	return sessionFromRow(row), nil
}

// List returns every session, newest first, optionally filtered to one state.
// A plain list rather than cursor-paginated: session counts never approach
// library scale (Invariant 22 is about files, not import runs).
func (s ImportSessions) List(ctx context.Context, filterState string) ([]Session, error) {
	rows, err := librarydb.New(s.store.Read()).ListImportSessions(ctx, librarydb.ListImportSessionsParams{
		FilterByState: boolToInt(filterState != ""), State: filterState,
	})
	if err != nil {
		return nil, fmt.Errorf("library: list import sessions: %w", err)
	}
	sessions := make([]Session, len(rows))
	for i, row := range rows {
		sessions[i] = sessionFromRow(row)
	}
	return sessions, nil
}

// ListUnits returns every unit one session found, in the order they were added.
func (s ImportSessions) ListUnits(ctx context.Context, sessionID int64) ([]Unit, error) {
	rows, err := librarydb.New(s.store.Read()).ListImportUnits(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("library: list units for import session %d: %w", sessionID, err)
	}
	units := make([]Unit, len(rows))
	for i, row := range rows {
		units[i] = unitFromRow(row)
	}
	return units, nil
}

func sessionFromRow(row librarydb.ImportSession) Session {
	return Session{
		ID: row.ID, SourcePath: row.SourcePath, Origin: row.Origin,
		AcquisitionID: int64Ptr(row.AcquisitionID), CategoryID: int64Ptr(row.CategoryID),
		State: row.State, Threshold: row.Threshold, UnitsTotal: row.UnitsTotal,
		UnitsCommitted: row.UnitsCommitted, UnitsQueued: row.UnitsQueued,
		BytesImported: row.BytesImported, ExtractDir: row.ExtractDir.String,
		StartedAt: row.StartedAt, FinishedAt: int64Ptr(row.FinishedAt), Error: row.Error.String,
	}
}

func unitFromRow(row librarydb.ImportUnit) Unit {
	return Unit{
		ID: row.ID, SessionID: row.SessionID, RelPath: row.RelPath, Shape: row.Shape,
		FileCount: row.FileCount, Bytes: row.Bytes, Confidence: row.Confidence,
		Decision: row.Decision, WorkID: int64Ptr(row.WorkID), EditionID: int64Ptr(row.EditionID),
		EvidenceJSON: row.EvidenceJson, CandidatesJSON: row.CandidatesJson.String, Reason: row.Reason.String,
	}
}

func int64Ptr(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}
