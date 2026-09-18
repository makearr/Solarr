package library

import (
	"context"
	"errors"
	"fmt"
	"path"
	"time"

	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// ErrSessionNotRevertible reports that sessionID is not in a state Revert
// can act on -- only a session that finished inspecting (successfully or
// not) has anything of its own to undo; one still INSPECTING/IMPORTING/
// CANCELLING is not done deciding what it did, and one already REVERTED or
// CANCELLED has nothing left.
var ErrSessionNotRevertible = errors.New("library: import session cannot be reverted from its current state")

// RevertResult is what one session's revert actually did.
type RevertResult struct {
	QuarantinedPaths []Entry
	UnitsReverted    int
	WorksDeleted     int
	EditionsDeleted  int
	// SkippedSharedObjects are media_object ids revert left untouched: bytes
	// this unit shares with a path from outside this session (ADR-11's
	// quick-hash dedupe), recorded before migration 0029 gave revert a way to
	// tell its own path apart from the other one -- refusing to guess rather
	// than risk quarantining and deleting a library entry the session never
	// created (Invariant 92).
	SkippedSharedObjects []int64
}

// ImportRevert is ADR-48's own "revert this session": move every file the
// session placed back through quarantine (section 10.8) and remove exactly
// the rows the session created, never a row it only matched (ADR-48's own
// wording) -- import_unit.work_created/edition_created say which is which,
// RecordPlacement's own doc comment explains why they exist. Composes
// ImportSessions, Physical and Quarantine the same way RemoveAndBlock
// composes its own four stores; not a new kind of orchestration, the same
// one, aimed at an import session instead of one file.
type ImportRevert struct {
	sessions   ImportSessions
	physical   Physical
	identity   Identity
	quarantine Quarantine
	rootPath   RootFolderPath
}

// NewImportRevert builds it over the stores it composes.
func NewImportRevert(
	sessions ImportSessions, physical Physical, identity Identity, quarantine Quarantine, rootPath RootFolderPath,
) ImportRevert {
	return ImportRevert{
		sessions: sessions, physical: physical, identity: identity, quarantine: quarantine, rootPath: rootPath,
	}
}

// Revert undoes sessionID's own COMMIT placements. A unit DECIDE queued or
// skipped, or one COMMIT-decided but never actually placed (no category_id
// to place into, or a placement failure that was logged and tolerated), has
// nothing to undo and is left exactly as it was -- only WorkID != nil marks
// a unit this needs to act on.
//
// Two passes over the session's own units, deliberately not one: the first
// quarantines every file and clears every unit's own work_id/edition_id
// (ClearImportUnitPlacement), the second deletes the work/edition rows
// themselves. A work two units in the same session both resolved to (one
// created it, the second only matched it) would foreign-key-violate if the
// first unit tried to delete "its own" work while the second unit's own
// import_unit row still pointed at it -- clearing every unit's reference
// first, before any row is deleted, is what makes deletion order not
// matter.
func (r ImportRevert) Revert(ctx context.Context, sessionID int64, retain time.Duration, at int64) (RevertResult, error) {
	session, err := r.sessions.Get(ctx, sessionID)
	if err != nil {
		return RevertResult{}, fmt.Errorf("library: revert session %d: %w", sessionID, err)
	}
	if session.State != ImportDone && session.State != ImportFailed {
		return RevertResult{}, fmt.Errorf("%w: session %d is %s", ErrSessionNotRevertible, sessionID, session.State)
	}

	units, err := r.sessions.ListUnits(ctx, sessionID)
	if err != nil {
		return RevertResult{}, fmt.Errorf("library: revert session %d: list units: %w", sessionID, err)
	}
	unitFiles, err := r.sessions.UnitFiles(ctx, sessionID)
	if err != nil {
		return RevertResult{}, fmt.Errorf("library: revert session %d: list unit files: %w", sessionID, err)
	}

	var result RevertResult
	var placed []Unit
	for _, unit := range units {
		if unit.WorkID == nil {
			continue
		}
		placed = append(placed, unit)

		for _, file := range unitFiles[unit.ID] {
			if err := r.quarantineObject(ctx, file, retain, &result); err != nil {
				return result, fmt.Errorf("library: revert unit %d: %w", unit.ID, err)
			}
		}
		if err := r.sessions.ClearUnitPlacement(ctx, unit.ID); err != nil {
			return result, fmt.Errorf("library: revert unit %d: clear placement: %w", unit.ID, err)
		}
		result.UnitsReverted++
	}

	for _, unit := range placed {
		if unit.EditionCreated && unit.EditionID != nil {
			if err := r.identity.DeleteEdition(ctx, *unit.EditionID); err != nil {
				return result, fmt.Errorf("library: revert unit %d: %w", unit.ID, err)
			}
			result.EditionsDeleted++
		}
		if unit.WorkCreated {
			if err := r.identity.DeleteWork(ctx, *unit.WorkID); err != nil {
				return result, fmt.Errorf("library: revert unit %d: %w", unit.ID, err)
			}
			result.WorksDeleted++
		}
	}

	if err := r.sessions.Finish(ctx, sessionID, ImportReverted, at, ""); err != nil {
		return result, fmt.Errorf("library: revert session %d: mark reverted: %w", sessionID, err)
	}
	return result, nil
}

// quarantineObject quarantines exactly the one library_path row this unit's own
// placement created for file.MediaObjectID, then deletes the media_object outright
// only if that was its last remaining path.
//
// ADR-11's quick-hash dedupe means RegisterBatch can point this unit's placement at
// bytes an unrelated, untouched commit already owns (a duplicate track shipped in two
// releases, say). The naive shape this replaced -- quarantine every PathsForObject row
// and delete the object -- destroyed that untouched library entry too whenever it
// happened. file.LibraryPathID (migration 0029) is what lets this pick out only the
// session's own row; when it is nil (data from before that migration) or PathsForObject
// no longer contains it, this refuses to guess among more than one candidate rather
// than repeat the bug (Invariant 92), and records the skip instead.
func (r ImportRevert) quarantineObject(ctx context.Context, file UnitFile, retain time.Duration, result *RevertResult) error {
	paths, err := r.physical.PathsForObject(ctx, file.MediaObjectID)
	if err != nil {
		return fmt.Errorf("paths for object %d: %w", file.MediaObjectID, err)
	}

	target, ok := selectPathToRevert(paths, file.LibraryPathID)
	if !ok {
		result.SkippedSharedObjects = append(result.SkippedSharedObjects, file.MediaObjectID)
		return nil
	}

	rootPath, ok := r.rootPath(target.RootFolderID)
	if !ok {
		return fmt.Errorf("no known path for root folder %d", target.RootFolderID)
	}
	rootFolderID, mediaObjectID := target.RootFolderID, file.MediaObjectID
	entry, err := r.quarantine.Put(ctx, PutRequest{
		RootPath: rootPath, OriginalPath: path.Join(rootPath, target.RelPath),
		RootFolderID: &rootFolderID, MediaObjectID: &mediaObjectID,
		Reason: ReasonFailedImport, Retain: retain,
	})
	if err != nil {
		if !errors.Is(err, ErrCrossDevice) {
			return fmt.Errorf("quarantine %q: %w", target.RelPath, err)
		}
		// Already gone (deleted directly, Quarantine's own doc comment) -- nothing to
		// restore for this copy, but the row (and, below, maybe the object) is still
		// cleaned up.
	} else {
		result.QuarantinedPaths = append(result.QuarantinedPaths, entry)
	}

	if len(paths) == 1 {
		return r.physical.DeleteObject(ctx, file.MediaObjectID)
	}
	return r.physical.DeleteLibraryPathRow(ctx, target.ID)
}

// selectPathToRevert picks the one row among paths that belongs to this unit's own
// placement. A single path is unambiguous regardless of libraryPathID; more than one
// requires knowing which, and refuses (ok=false) when it cannot.
func selectPathToRevert(paths []librarydb.LibraryPath, libraryPathID *int64) (librarydb.LibraryPath, bool) {
	if len(paths) == 1 {
		return paths[0], true
	}
	if libraryPathID == nil {
		return librarydb.LibraryPath{}, false
	}
	for _, p := range paths {
		if p.ID == *libraryPathID {
			return p, true
		}
	}
	return librarydb.LibraryPath{}, false
}
