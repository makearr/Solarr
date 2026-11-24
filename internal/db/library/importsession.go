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

// Import unit shapes (import_unit.shape) re-export inspect.Shape values as strings; UnitPack has no equivalent because PACK is a cross-unit fact (ADR-50).
const (
	UnitSingle    = string(inspect.ShapeSingle)
	UnitSet       = string(inspect.ShapeSet)
	UnitPack      = "PACK"
	UnitMultiDisc = string(inspect.ShapeMultiDisc)
)

// Import unit decisions (import_unit.decision) re-export model.UnitDecision values; a unit is UnitQueue until IDENTIFY/DECIDE records otherwise.
const (
	UnitCommit = string(model.DecisionCommit)
	UnitQueue  = string(model.DecisionQueue)
	UnitSkip   = string(model.DecisionSkip)
	UnitFailed = string(model.DecisionFailed)
)

// UnitReasonDuplicate is the reason of a skipped unit whose pictures the library already holds.
const UnitReasonDuplicate = "its picture is one the library already holds, and it is no better"

// UnitReasonBelowProfile is the reason of a skipped unit whose probed files no quality profile of its library accepts, for
// a release that named no format; the download is blocklisted and the next release is grabbed.
const UnitReasonBelowProfile = "its files, once probed, meet no quality profile of the library"

// ImportSessions is ADR-48's persistence of inspection-and-import runs and their units, so a session survives the process and a revert has something to undo (inspect types stay in memory, ADR-23).
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
	// WorkCreated/EditionCreated record whether CommitUnit created the row or matched one, false until RecordPlacement; a revert deletes only created rows (ADR-48).
	WorkCreated    bool
	EditionCreated bool
	// PackID is non-nil once todo 4.11's own detection (run once, after WALK
	// finishes) finds this unit belongs to a pack -- nil is the overwhelming
	// common case (a single-release import).
	PackID *int64
	// CategoryID is the library a Best match import routed the unit to; nil places it into the session's.
	CategoryID *int64
}

// PlacesInto is the library the unit is placed into: its own, else its session's; nil while neither has one.
func (u Unit) PlacesInto(session Session) *int64 {
	if u.CategoryID != nil {
		return u.CategoryID
	}
	return session.CategoryID
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
	// CategoryID is the library a Best match import routed it to; nil for one routed by its session.
	CategoryID *int64
	// Media are the unit's media files, kept when it shares its folder with other units; empty keeps none.
	Media []UnitMedia
}

// UnitMedia is one media file of a unit, its path relative to the unit's own folder.
type UnitMedia struct {
	RelPath   string
	SizeBytes int64
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

// AddUnits records every unit of one inspection pass in one transaction (Invariant 2), recomputes the session counts (drift-and-repair), and returns ids in unit order for an immediate SetUnitDecision.
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
				CategoryID: nullInt64(unit.CategoryID),
			})
			if err != nil {
				return fmt.Errorf("insert unit %s: %w", unit.RelPath, err)
			}
			ids[i] = row.ID
			for _, file := range unit.Media {
				if err := queries.InsertImportUnitMedia(ctx, librarydb.InsertImportUnitMediaParams{
					UnitID: row.ID, RelPath: file.RelPath, SizeBytes: file.SizeBytes,
				}); err != nil {
					return fmt.Errorf("insert media of unit %s: %w", unit.RelPath, err)
				}
			}
		}
		return queries.RecomputeImportSessionCounts(ctx, sessionID)
	})
	if err != nil {
		return nil, fmt.Errorf("library: add import units to session %d: %w", sessionID, err)
	}
	return ids, nil
}

// DeleteUnits removes units a pass recorded but could not finish, so a resumed walk, which starts after the last unit
// recorded, inspects them again.
func (s ImportSessions) DeleteUnits(ctx context.Context, sessionID int64, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	err := s.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		if err := queries.DeleteImportUnits(ctx, librarydb.DeleteImportUnitsParams{SessionID: sessionID, Ids: ids}); err != nil {
			return err
		}
		return queries.RecomputeImportSessionCounts(ctx, sessionID)
	})
	if err != nil {
		return fmt.Errorf("library: delete import units of session %d: %w", sessionID, err)
	}
	return nil
}

// UnitDecision is one unit's IDENTIFY/DECIDE result, ready to record.
type UnitDecision struct {
	UnitID                 int64
	Decision               string
	Confidence             float64
	WorkID, EditionID      *int64
	CandidatesJSON, Reason string
}

// SetUnitDecisions records a cluster's decisions in one transaction with one count recompute (Invariant 3, todo 4.21), instead of N reads and transactions.
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

// Placement is one unit's CommitUnit result: the import_unit columns plus every media_object row import_unit_file must remember for a revert (ADR-48).
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
	// Release is what was known of the release the files came in, kept with each file; nil when nothing was.
	Release *ReleaseOrigin
}

// ReleaseOrigin is what Solarr knew of a release when it grabbed it (QUAL-1): the library file's own name may say none of it.
type ReleaseOrigin struct {
	Title, Source, Group string
	// Revision is 1 for an original, 2 and up for a PROPER, REPACK or vN.
	Revision  int
	IndexerID *int64
	GrabbedAt int64
}

// UnitExtra is one extra file an import placed, by root folder and path under it.
type UnitExtra struct {
	RootFolderID int64
	RelPath      string
}

// RecordPlacement writes a unit's placement in one transaction; work_id non-null is what "actually placed" means, plus one import_unit_file row per object.
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
		if placement.Release != nil {
			for _, mediaObjectID := range placement.MediaObjectIDs {
				if err := queries.UpsertMediaObjectRelease(ctx, librarydb.UpsertMediaObjectReleaseParams{
					MediaObjectID: mediaObjectID, ReleaseTitle: placement.Release.Title, Source: placement.Release.Source,
					ReleaseGroup: placement.Release.Group, Revision: int64(max(placement.Release.Revision, 1)),
					IndexerID: nullInt64(placement.Release.IndexerID), GrabbedAt: placement.Release.GrabbedAt,
				}); err != nil {
					return fmt.Errorf("record release of file %d: %w", mediaObjectID, err)
				}
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

// DeleteUnitExtras forgets a unit's extra files once revert has recycled them.
func (s ImportSessions) DeleteUnitExtras(ctx context.Context, unitID int64) error {
	err := s.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).DeleteImportUnitExtras(ctx, unitID)
	})
	if err != nil {
		return fmt.Errorf("library: delete unit %d extras: %w", unitID, err)
	}
	return nil
}

// ClearUnitPlacement is revert's first step per unit: it restores the pre-placement values so the work/edition can be deleted (import_unit's FKs have no cascade).
func (s ImportSessions) ClearUnitPlacement(ctx context.Context, unitID int64) error {
	err := s.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).ClearImportUnitPlacement(ctx, unitID)
	})
	if err != nil {
		return fmt.Errorf("library: clear unit %d placement: %w", unitID, err)
	}
	return nil
}

// ReleaseAndDelete forgets the placement of the units and deletes the edition and work they made, in one transaction:
// the units point at the rows, so neither can go first, and a stop between would lose what the unit made.
func (s ImportSessions) ReleaseAndDelete(ctx context.Context, unitIDs []int64, editionID, workID *int64) error {
	err := s.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		for _, unitID := range unitIDs {
			if err := queries.ClearImportUnitPlacement(ctx, unitID); err != nil {
				return err
			}
		}
		if editionID != nil {
			if err := queries.DeleteEdition(ctx, *editionID); err != nil {
				return err
			}
		}
		if workID == nil {
			return nil
		}
		members, err := membersOfWork(ctx, queries, *workID)
		if err != nil {
			return err
		}
		if err := deleteWorkRow(ctx, queries, *workID); err != nil {
			return err
		}
		return members.refresh(ctx, queries)
	})
	if err != nil {
		return fmt.Errorf("library: release units %v and delete what they made: %w", unitIDs, err)
	}
	return nil
}

// UnitFile is a media_object row a unit's commit produced with the library_path it created; LibraryPathID is nil before migration 0029 or when the object already had several paths.
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

// UnitTransferMethods reports per unit how files were placed (link or copy), invisible on disk; rows before migration 0034 have no entry.
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

// dismissBatch bounds one write when many sessions are hidden at once.
const dismissBatch = 200

// DismissFinished hides every stopped session that holds nothing for review, a batch per write, and answers how many.
func (s ImportSessions) DismissFinished(ctx context.Context, at int64) (int64, error) {
	var total int64
	for {
		var affected int64
		err := s.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			rows, err := librarydb.New(tx).DismissFinishedImportSessions(ctx, librarydb.DismissFinishedImportSessionsParams{
				DismissedAt: sql.NullInt64{Int64: at, Valid: true}, BatchSize: dismissBatch,
			})
			affected = rows
			return err
		})
		if err != nil {
			return total, fmt.Errorf("library: dismiss finished import sessions: %w", err)
		}
		total += affected
		if affected < dismissBatch {
			return total, nil
		}
	}
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
// LatestForAcquisition answers the newest import started for a download; sql.ErrNoRows, wrapped,
// when there is none.
func (s ImportSessions) LatestForAcquisition(ctx context.Context, acquisitionID int64) (Session, error) {
	row, err := librarydb.New(s.store.Read()).GetLatestImportSessionForAcquisition(ctx,
		sql.NullInt64{Int64: acquisitionID, Valid: true})
	if err != nil {
		return Session{}, fmt.Errorf("library: import for acquisition %d: %w", acquisitionID, err)
	}
	return sessionFromRow(row), nil
}

// ListForAcquisition answers every import started for a download, oldest first.
func (s ImportSessions) ListForAcquisition(ctx context.Context, acquisitionID int64) ([]Session, error) {
	rows, err := librarydb.New(s.store.Read()).ListImportSessionsForAcquisition(ctx,
		sql.NullInt64{Int64: acquisitionID, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("library: imports for acquisition %d: %w", acquisitionID, err)
	}
	sessions := make([]Session, len(rows))
	for i, row := range rows {
		sessions[i] = sessionFromRow(row)
	}
	return sessions, nil
}

func (s ImportSessions) Get(ctx context.Context, id int64) (Session, error) {
	row, err := librarydb.New(s.store.Read()).GetImportSession(ctx, id)
	if err != nil {
		return Session{}, fmt.Errorf("library: get import session %d: %w", id, err)
	}
	return sessionFromRow(row), nil
}

// ErrSessionCategoryAlreadySet is a session already has a different category; re-pointing is out of scope, while the same categoryID again is not an error.
var ErrSessionCategoryAlreadySet = errors.New("library: import session already has a category")

// SetCategory assigns a category to a session that had none, idempotently, so ManualCommitter.PlaceUnplaced can be retried with the identical request after a transient placement failure.
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

// SessionPage asks for one page of sessions, newest first; BeforeID is the cursor's id tie-break.
type SessionPage struct {
	State                       string
	IncludeDismissed, Attention bool
	// RecentSince is the start time from which a manual import still counts as needing attention.
	RecentSince int64
	// HasCursor makes BeforeStartedAt and BeforeID the row the page starts after.
	HasCursor       bool
	BeforeStartedAt int64
	BeforeID        int64
	Limit           int64
}

// ListPage answers one page of sessions.
func (s ImportSessions) ListPage(ctx context.Context, page SessionPage) ([]Session, error) {
	rows, err := librarydb.New(s.store.Read()).ListImportSessionsPage(ctx, librarydb.ListImportSessionsPageParams{
		FilterByState: boolToInt(page.State != ""), State: page.State,
		IncludeDismissed: boolToInt(page.IncludeDismissed), Attention: boolToInt(page.Attention),
		RecentSince: page.RecentSince, HasCursor: boolToInt(page.HasCursor),
		BeforeStartedAt: page.BeforeStartedAt, BeforeID: page.BeforeID, RowLimit: page.Limit,
	})
	if err != nil {
		return nil, fmt.Errorf("library: list import sessions page: %w", err)
	}
	sessions := make([]Session, len(rows))
	for i, row := range rows {
		sessions[i] = sessionFromRow(row)
	}
	return sessions, nil
}

// DeleteOldSessions removes up to limit finished sessions older than finishedBefore that placed nothing still in the
// library and hold nothing for review, and answers how many went.
func (s ImportSessions) DeleteOldSessions(ctx context.Context, finishedBefore int64, limit int64) (int64, error) {
	var deleted int64
	err := s.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		deleted, err = librarydb.New(tx).DeleteOldImportSessions(ctx, librarydb.DeleteOldImportSessionsParams{
			FinishedBefore: sql.NullInt64{Int64: finishedBefore, Valid: true}, RowLimit: limit,
		})
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("library: delete old import sessions: %w", err)
	}
	return deleted, nil
}

// ListBySourcePath answers the imports of one source folder in one state, newest first, dismissed ones included.
func (s ImportSessions) ListBySourcePath(ctx context.Context, sourcePath, state string) ([]Session, error) {
	rows, err := librarydb.New(s.store.Read()).ListImportSessionsBySourcePath(ctx,
		librarydb.ListImportSessionsBySourcePathParams{SourcePath: sourcePath, State: state})
	if err != nil {
		return nil, fmt.Errorf("library: list %s imports of %s: %w", state, sourcePath, err)
	}
	sessions := make([]Session, len(rows))
	for i, row := range rows {
		sessions[i] = sessionFromRow(row)
	}
	return sessions, nil
}

// ListUnitMedia answers the media files kept for a unit; none for a unit that shares no folder or predates them.
func (s ImportSessions) ListUnitMedia(ctx context.Context, unitID int64) ([]UnitMedia, error) {
	rows, err := librarydb.New(s.store.Read()).ListImportUnitMedia(ctx, unitID)
	if err != nil {
		return nil, fmt.Errorf("library: list media of import unit %d: %w", unitID, err)
	}
	files := make([]UnitMedia, len(rows))
	for i, row := range rows {
		files[i] = UnitMedia{RelPath: row.RelPath, SizeBytes: row.SizeBytes}
	}
	return files, nil
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

// ListUnitsPage is cursor-paginated because a session's unit count is unbounded (Invariant 22, whole-library adoption); empty filterDecision means all.
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

// ListUnplacedCommitUnits answers units decided COMMIT but not placed (work_id still NULL), for the assign-a-category-later action (todo 4.9).
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

// SessionsWithUnplacedCommits pages the finished sessions holding a unit their library could not take yet, by id.
func (s ImportSessions) SessionsWithUnplacedCommits(ctx context.Context, afterID, limit int64) ([]int64, error) {
	ids, err := librarydb.New(s.store.Read()).ListSessionsWithUnplacedCommits(ctx, librarydb.ListSessionsWithUnplacedCommitsParams{
		AfterID: afterID, PageSize: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("library: list sessions with unplaced units: %w", err)
	}
	return ids, nil
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
		PackID: int64Ptr(row.PackID), CategoryID: int64Ptr(row.CategoryID),
	}
}

func int64Ptr(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}
