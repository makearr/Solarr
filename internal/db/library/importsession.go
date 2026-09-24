package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/importing/inspect"
)

// Import session states (import_session.state, migration 0018).
// ImportCancelling/ImportCancelled reuse this same column (migration 0023) --
// only the job itself writes ImportCancelled, after it stops recording units.
const (
	ImportInspecting = "INSPECTING"
	ImportImporting  = "IMPORTING"
	ImportCancelling = "CANCELLING"
	ImportDone       = "DONE"
	ImportFailed     = "FAILED"
	ImportReverted   = "REVERTED"
	ImportCancelled  = "CANCELLED"
)

// Import session origins (import_session.origin).
const (
	ImportOriginManual      = "manual"
	ImportOriginAcquisition = "acquisition"
	ImportOriginWatched     = "watched"
	ImportOriginAdoption    = "adoption"
)

// Import unit shapes (import_unit.shape) -- inspect.Shape's own values
// (inspect.ShapeOf), re-exported as plain strings for this file's own callers.
// UnitPack has no inspect.Shape equivalent: PACK is a cross-unit fact (ADR-50),
// never one unit's own structural shape (see inspect.Shape's doc comment).
const (
	UnitSingle    = string(inspect.ShapeSingle)
	UnitSet       = string(inspect.ShapeSet)
	UnitPack      = "PACK"
	UnitMultiDisc = string(inspect.ShapeMultiDisc)
)

// Import unit decisions (import_unit.decision) -- model.UnitDecision's own
// values (model.Decide), re-exported as plain strings for this file's own
// callers. A unit is recorded UnitQueue by default until a real IDENTIFY/DECIDE
// caller wires music.IdentifyUnit and model.Decide to SetUnitDecision.
const (
	UnitCommit = string(model.DecisionCommit)
	UnitQueue  = string(model.DecisionQueue)
	UnitSkip   = string(model.DecisionSkip)
	UnitFailed = string(model.DecisionFailed)
)

// ImportSessions is ADR-48's own persistence: one row per inspection-and-import
// run, and one row per unit it found. internal/importing/inspect.Session/Unit are pure and
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
	UnitsSkipped   int64
	UnitsFailed    int64
	BytesImported  int64
	ExtractDir     string
	StartedAt      int64
	FinishedAt     *int64
	Error          string
	// UnitsEstimated is nil until inspect.EstimateUnits has run once for this
	// session (todo 4.21's ETA) -- distinct from UnitsTotal, which is "found
	// so far", not a real total.
	UnitsEstimated *int64
	// DismissedAt is nil while the session is still shown on the Import
	// screen. Dismissal hides the row, it never deletes it.
	DismissedAt *int64
	// Preview holds every unit for review; a resumed run keeps it, as it keeps Threshold.
	Preview bool
}

// NewSession is what Create needs to start one run.
type NewSession struct {
	SourcePath    string
	Origin        string
	AcquisitionID *int64
	CategoryID    *int64
	Threshold     float64
	StartedAt     int64
	Preview       bool
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
	// WorkCreated/EditionCreated record whether this unit's own CommitUnit
	// created the row or matched an existing one -- both false until
	// RecordPlacement runs. A later session revert must only ever delete a
	// row the session itself created (ADR-48).
	WorkCreated    bool
	EditionCreated bool
	// PackID is non-nil once todo 4.11's own detection (run once, after WALK
	// finishes) finds this unit belongs to a pack -- nil is the overwhelming
	// common case (a single-release import).
	PackID *int64
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
			Threshold: params.Threshold, StartedAt: params.StartedAt, Preview: boolToInt(params.Preview),
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
// change a unit's decision. Returns the new rows' ids, in the same order as
// units, so a caller that already has a real IDENTIFY/DECIDE result in hand
// (import.inspect's own job) can call SetUnitDecision immediately without a
// second round trip to look them back up.
func (s ImportSessions) AddUnits(ctx context.Context, sessionID int64, units []NewUnit) ([]int64, error) {
	if len(units) == 0 {
		return nil, nil
	}
	ids := make([]int64, len(units))
	err := s.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		for i, unit := range units {
			row, err := queries.InsertImportUnit(ctx, librarydb.InsertImportUnitParams{
				SessionID: sessionID, RelPath: unit.RelPath, Shape: unit.Shape,
				FileCount: unit.FileCount, Bytes: unit.Bytes, Confidence: 0,
				Decision: UnitQueue, EvidenceJson: unit.EvidenceJSON, Reason: nullString(unit.Reason),
			})
			if err != nil {
				return fmt.Errorf("insert unit %s: %w", unit.RelPath, err)
			}
			ids[i] = row.ID
		}
		return queries.RecomputeImportSessionCounts(ctx, sessionID)
	})
	if err != nil {
		return nil, fmt.Errorf("library: add import units to session %d: %w", sessionID, err)
	}
	return ids, nil
}

// UnitDecision is one unit's IDENTIFY/DECIDE result, ready to record.
type UnitDecision struct {
	UnitID                 int64
	Decision               string
	Confidence             float64
	WorkID, EditionID      *int64
	CandidatesJSON, Reason string
}

// SetUnitDecisions records every unit one cluster's IDENTIFY step decided, in
// a single transaction with a single RecomputeImportSessionCounts at the end
// (Invariant 3, todo 4.21) -- the same batching AddUnits already gives the
// insert half, instead of the N reads and N transactions calling this once
// per unit cost. The caller already knows sessionID (it is the cluster's own),
// so this needs no per-unit lookup the way the single-unit version once did.
func (s ImportSessions) SetUnitDecisions(ctx context.Context, sessionID int64, decisions []UnitDecision) error {
	if len(decisions) == 0 {
		return nil
	}
	err := s.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		for _, d := range decisions {
			if err := queries.SetImportUnitDecision(ctx, librarydb.SetImportUnitDecisionParams{
				ID: d.UnitID, Decision: d.Decision, Confidence: d.Confidence,
				WorkID: nullInt64(d.WorkID), EditionID: nullInt64(d.EditionID),
				CandidatesJson: nullString(d.CandidatesJSON), Reason: nullString(d.Reason),
			}); err != nil {
				return fmt.Errorf("set unit %d decision: %w", d.UnitID, err)
			}
		}
		return queries.RecomputeImportSessionCounts(ctx, sessionID)
	})
	if err != nil {
		return fmt.Errorf("library: set import unit decisions for session %d: %w", sessionID, err)
	}
	return nil
}

// Placement is one unit's own CommitUnit result, ready to record --
// import_unit's own work_id/edition_id/work_created/edition_created columns
// plus every media_object row import_unit_file must remember, so a later
// session revert (ADR-48) knows precisely which rows this unit's own commit
// is responsible for.
type Placement struct {
	WorkID         int64
	WorkCreated    bool
	EditionID      *int64
	EditionCreated bool
	MediaObjectIDs []int64
	// LibraryPathIDs is parallel to MediaObjectIDs -- see Placed's own doc
	// comment (internal/media/music). Left nil by a caller that cannot supply
	// it; ImportRevert then falls back to its own unambiguous-only rule.
	LibraryPathIDs []int64
	// TransferMethods is parallel to MediaObjectIDs too: which ladder rung
	// placed each file, left nil by a caller that does not know.
	TransferMethods []string
	// Extras are the extra files placed beside the media, so revert can find them.
	Extras []UnitExtra
}

// UnitExtra is one extra file an import placed, by root folder and path under it.
type UnitExtra struct {
	RootFolderID int64
	RelPath      string
}

// RecordPlacement writes one unit's placement, in one transaction: the
// import_unit columns (work_id becoming non-null is what "actually placed"
// means from here on, not merely decided -- job.go's own doc comment) and
// one import_unit_file row per media_object CommitUnit placed.
func (s ImportSessions) RecordPlacement(ctx context.Context, unitID int64, placement Placement) error {
	return s.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		if err := queries.SetImportUnitPlacement(ctx, librarydb.SetImportUnitPlacementParams{
			ID: unitID, WorkID: sql.NullInt64{Int64: placement.WorkID, Valid: true},
			EditionID:      nullInt64(placement.EditionID),
			WorkCreated:    boolToInt(placement.WorkCreated),
			EditionCreated: boolToInt(placement.EditionCreated),
		}); err != nil {
			return fmt.Errorf("set unit %d placement: %w", unitID, err)
		}
		for index, mediaObjectID := range placement.MediaObjectIDs {
			var libraryPathID *int64
			if index < len(placement.LibraryPathIDs) {
				libraryPathID = &placement.LibraryPathIDs[index]
			}
			var transferMethod sql.NullString
			if index < len(placement.TransferMethods) && placement.TransferMethods[index] != "" {
				transferMethod = sql.NullString{String: placement.TransferMethods[index], Valid: true}
			}
			if err := queries.InsertImportUnitFile(ctx, librarydb.InsertImportUnitFileParams{
				UnitID: unitID, MediaObjectID: mediaObjectID, LibraryPathID: nullInt64(libraryPathID),
				TransferMethod: transferMethod,
			}); err != nil {
				return fmt.Errorf("insert unit %d file %d: %w", unitID, mediaObjectID, err)
			}
		}
		for _, extra := range placement.Extras {
			if err := queries.InsertImportUnitExtra(ctx, librarydb.InsertImportUnitExtraParams{
				UnitID: unitID, RootFolderID: extra.RootFolderID, RelPath: extra.RelPath,
			}); err != nil {
				return fmt.Errorf("insert unit %d extra %q: %w", unitID, extra.RelPath, err)
			}
		}
		return nil
	})
}

// UnitExtras maps every unit in a session to the extra files its placement brought along.
func (s ImportSessions) UnitExtras(ctx context.Context, sessionID int64) (map[int64][]UnitExtra, error) {
	rows, err := librarydb.New(s.store.Read()).ListImportUnitExtrasForSession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("library: list unit extras for session %d: %w", sessionID, err)
	}
	byUnit := make(map[int64][]UnitExtra, len(rows))
	for _, row := range rows {
		byUnit[row.UnitID] = append(byUnit[row.UnitID], UnitExtra{RootFolderID: row.RootFolderID, RelPath: row.RelPath})
	}
	return byUnit, nil
}

// DeleteUnitExtras forgets a unit's extra files once revert has quarantined them.
func (s ImportSessions) DeleteUnitExtras(ctx context.Context, unitID int64) error {
	err := s.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).DeleteImportUnitExtras(ctx, unitID)
	})
	if err != nil {
		return fmt.Errorf("library: delete unit %d extras: %w", unitID, err)
	}
	return nil
}

// ClearUnitPlacement is revert's own first step for one unit: work_id/
// edition_id/work_created/edition_created all go back to their pre-placement
// values, which is what makes it safe to then delete the work/edition row
// itself (RecordPlacement's own doc comment: import_unit's FK to either
// carries no ON DELETE CASCADE).
func (s ImportSessions) ClearUnitPlacement(ctx context.Context, unitID int64) error {
	err := s.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).ClearImportUnitPlacement(ctx, unitID)
	})
	if err != nil {
		return fmt.Errorf("library: clear unit %d placement: %w", unitID, err)
	}
	return nil
}

// UnitFile is one media_object row a unit's own CommitUnit produced, with the
// specific library_path row that placement created for it when known
// (LibraryPathID is nil for a row written before migration 0029, or one whose
// media_object already had more than one path at that time -- see the
// migration's own comment).
type UnitFile struct {
	MediaObjectID int64
	LibraryPathID *int64
	// TransferMethod is which rung of the import ladder placed this file, or
	// empty for a row written before the method was recorded (migration 0034).
	TransferMethod string
}

// UnitFiles maps every placed unit in a session to the media_object rows its
// own CommitUnit produced (import_unit_file) -- revert's own map of exactly
// what to undo, grouped once per session rather than one query per unit.
func (s ImportSessions) UnitFiles(ctx context.Context, sessionID int64) (map[int64][]UnitFile, error) {
	rows, err := librarydb.New(s.store.Read()).ListImportUnitFilesForSession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("library: list unit files for session %d: %w", sessionID, err)
	}
	byUnit := make(map[int64][]UnitFile, len(rows))
	for _, row := range rows {
		var libraryPathID *int64
		if row.LibraryPathID.Valid {
			libraryPathID = &row.LibraryPathID.Int64
		}
		byUnit[row.UnitID] = append(byUnit[row.UnitID], UnitFile{
			MediaObjectID: row.MediaObjectID, LibraryPathID: libraryPathID,
			TransferMethod: row.TransferMethod.String,
		})
	}
	return byUnit, nil
}

// TransferMethodCount is how many of a unit's files arrived by one rung.
type TransferMethodCount struct {
	Method string
	Files  int64
}

// UnitTransferMethods reports, per unit in a session, how its files were
// actually placed -- the answer to "did it link them or copy them?", which is
// invisible on disk (a reflink and a copy look identical). Units whose rows
// predate migration 0034 simply have no entry.
func (s ImportSessions) UnitTransferMethods(
	ctx context.Context, sessionID int64,
) (map[int64][]TransferMethodCount, error) {
	rows, err := librarydb.New(s.store.Read()).CountImportUnitTransferMethodsForSession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("library: count unit transfer methods for session %d: %w", sessionID, err)
	}
	byUnit := make(map[int64][]TransferMethodCount, len(rows))
	for _, row := range rows {
		byUnit[row.UnitID] = append(byUnit[row.UnitID], TransferMethodCount{
			Method: row.TransferMethod.String, Files: row.FileCount,
		})
	}
	return byUnit, nil
}

// SetUnitsEstimated records inspect.EstimateUnits' one-off result (todo
// 4.21's ETA). Callers only call this once per session -- Job.Run skips it
// once Session.UnitsEstimated is non-nil.
func (s ImportSessions) SetUnitsEstimated(ctx context.Context, sessionID, estimate int64) error {
	err := s.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).SetImportSessionUnitsEstimated(ctx, librarydb.SetImportSessionUnitsEstimatedParams{
			ID: sessionID, UnitsEstimated: nullInt64(&estimate),
		})
	})
	if err != nil {
		return fmt.Errorf("library: set import session %d units estimated: %w", sessionID, err)
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

// ErrSessionNotCancellable reports that a session was not INSPECTING, so
// there was nothing running to ask to stop.
var ErrSessionNotCancellable = errors.New("library: import session is not running")

// RequestCancel only flips the flag the running job itself polls for; the
// job is what actually stops the walk and transitions to ImportCancelled.
func (s ImportSessions) RequestCancel(ctx context.Context, sessionID int64) error {
	var affected int64
	err := s.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		rows, err := librarydb.New(tx).RequestImportSessionCancel(ctx, sessionID)
		affected = rows
		return err
	})
	if err != nil {
		return fmt.Errorf("library: request cancel for import session %d: %w", sessionID, err)
	}
	if affected == 0 {
		return ErrSessionNotCancellable
	}
	return nil
}

// ErrSessionNotDismissable reports that a session has not finished, so there
// is nothing settled to hide, or that it is already hidden.
var ErrSessionNotDismissable = errors.New("library: import session has not finished")

// HasSessionFor reports whether sourcePath was ever imported, finished or dismissed alike.
func (s ImportSessions) HasSessionFor(ctx context.Context, sourcePath string) (bool, error) {
	count, err := librarydb.New(s.store.Read()).CountImportSessionsForSource(ctx, sourcePath)
	if err != nil {
		return false, fmt.Errorf("library: sessions for %q: %w", sourcePath, err)
	}
	return count > 0, nil
}

// Dismiss hides a finished session from the Import screen. The row stays: it
// is still the record of what the session placed, and a revert still reaches
// it by id.
func (s ImportSessions) Dismiss(ctx context.Context, sessionID, at int64) error {
	var affected int64
	err := s.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		rows, err := librarydb.New(tx).DismissImportSession(ctx, librarydb.DismissImportSessionParams{
			ID: sessionID, DismissedAt: sql.NullInt64{Int64: at, Valid: true},
		})
		affected = rows
		return err
	})
	if err != nil {
		return fmt.Errorf("library: dismiss import session %d: %w", sessionID, err)
	}
	if affected == 0 {
		return ErrSessionNotDismissable
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

// ErrSessionCategoryAlreadySet reports that a session already has a
// *different* category -- todo 4.9's "assign a category later" path is for a
// session that started with none (questions.md); re-pointing an
// already-targeted session at a different category is out of scope, not
// silently allowed. Calling SetCategory again with the *same* categoryID is
// not an error (see its own doc comment).
var ErrSessionCategoryAlreadySet = errors.New("library: import session already has a category")

// SetCategory assigns categoryID to a session that started without one, or
// is a no-op success when categoryID already matches -- deliberately
// idempotent so a caller can retry ManualCommitter.PlaceUnplaced (the
// adapter's own AssignImportCategory calls both together) after a transient
// placement failure, such as a root folder's capacity not having been
// probed yet, by sending the identical request again.
func (s ImportSessions) SetCategory(ctx context.Context, sessionID, categoryID int64) error {
	var affected int64
	err := s.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		rows, err := librarydb.New(tx).SetImportSessionCategory(ctx, librarydb.SetImportSessionCategoryParams{
			ID: sessionID, CategoryID: sql.NullInt64{Int64: categoryID, Valid: true},
		})
		affected = rows
		return err
	})
	if err != nil {
		return fmt.Errorf("library: set import session %d category: %w", sessionID, err)
	}
	if affected == 0 {
		return ErrSessionCategoryAlreadySet
	}
	return nil
}

// List returns every session, newest first, optionally filtered to one state.
// A plain list rather than cursor-paginated: session counts never approach
// library scale (Invariant 22 is about files, not import runs).
func (s ImportSessions) List(ctx context.Context, filterState string, includeDismissed bool) ([]Session, error) {
	rows, err := librarydb.New(s.store.Read()).ListImportSessions(ctx, librarydb.ListImportSessionsParams{
		FilterByState: boolToInt(filterState != ""), State: filterState,
		IncludeDismissed: boolToInt(includeDismissed),
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

// ListUnitsPage returns one cursor-paginated page of a session's units,
// unlike ListUnits above -- a session's own unit count is not bounded the way
// a session count is (Invariant 22 is exactly the "500k+ files" case a
// whole-library adoption run can hit), so an API caller needs a real page.
// filterDecision empty means every decision.
func (s ImportSessions) ListUnitsPage(
	ctx context.Context, sessionID, afterID int64, filterDecision string, pageSize int64,
) ([]Unit, error) {
	rows, err := librarydb.New(s.store.Read()).ListImportUnitsPage(ctx, librarydb.ListImportUnitsPageParams{
		SessionID: sessionID, AfterID: afterID,
		FilterByDecision: boolToInt(filterDecision != ""), Decision: filterDecision,
		PageSize: pageSize,
	})
	if err != nil {
		return nil, fmt.Errorf("library: list units page for import session %d: %w", sessionID, err)
	}
	units := make([]Unit, len(rows))
	for i, row := range rows {
		units[i] = unitFromRow(row)
	}
	return units, nil
}

// GetUnit answers one unit by id. A missing id surfaces sql.ErrNoRows, wrapped.
func (s ImportSessions) GetUnit(ctx context.Context, id int64) (Unit, error) {
	row, err := librarydb.New(s.store.Read()).GetImportUnit(ctx, id)
	if err != nil {
		return Unit{}, fmt.Errorf("library: get import unit %d: %w", id, err)
	}
	return unitFromRow(row), nil
}

// ListUnplacedCommitUnits answers every unit this session decided COMMIT but
// has not yet actually placed -- todo 4.9's "assign a category later" UI
// action needs these once a session gets a category it did not have while
// walking. work_id staying NULL after DECIDE is what distinguishes the two
// (job.go's own doc comment).
func (s ImportSessions) ListUnplacedCommitUnits(ctx context.Context, sessionID int64) ([]Unit, error) {
	rows, err := librarydb.New(s.store.Read()).ListUnplacedCommitUnits(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("library: list unplaced commit units for session %d: %w", sessionID, err)
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
		UnitsSkipped: row.UnitsSkipped, UnitsFailed: row.UnitsFailed,
		BytesImported: row.BytesImported, ExtractDir: row.ExtractDir.String,
		StartedAt: row.StartedAt, FinishedAt: int64Ptr(row.FinishedAt), Error: row.Error.String,
		UnitsEstimated: int64Ptr(row.UnitsEstimated), DismissedAt: int64Ptr(row.DismissedAt),
		Preview: row.Preview != 0,
	}
}

func unitFromRow(row librarydb.ImportUnit) Unit {
	return Unit{
		ID: row.ID, SessionID: row.SessionID, RelPath: row.RelPath, Shape: row.Shape,
		FileCount: row.FileCount, Bytes: row.Bytes, Confidence: row.Confidence,
		Decision: row.Decision, WorkID: int64Ptr(row.WorkID), EditionID: int64Ptr(row.EditionID),
		EvidenceJSON: row.EvidenceJson, CandidatesJSON: row.CandidatesJson.String, Reason: row.Reason.String,
		WorkCreated: row.WorkCreated != 0, EditionCreated: row.EditionCreated != 0,
		PackID: int64Ptr(row.PackID),
	}
}

func int64Ptr(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}
