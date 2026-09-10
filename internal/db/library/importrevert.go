package library

import (
	"context"
	"errors"
	"fmt"
	"path"
	"time"
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

		for _, mediaObjectID := range unitFiles[unit.ID] {
			if err := r.quarantineObject(ctx, mediaObjectID, retain, &result); err != nil {
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

// quarantineObject quarantines every library_path copy of one media_object
// (ADR-11 cross-entry hardlinking means there can be more than one) then
// deletes the object -- RemoveAndBlock.doQuarantine's own shape, without the
// block/blocklist/reopen steps a revert has no use for.
func (r ImportRevert) quarantineObject(ctx context.Context, mediaObjectID int64, retain time.Duration, result *RevertResult) error {
	paths, err := r.physical.PathsForObject(ctx, mediaObjectID)
	if err != nil {
		return fmt.Errorf("paths for object %d: %w", mediaObjectID, err)
	}
	for _, p := range paths {
		rootPath, ok := r.rootPath(p.RootFolderID)
		if !ok {
			return fmt.Errorf("no known path for root folder %d", p.RootFolderID)
		}
		rootFolderID := p.RootFolderID
		entry, err := r.quarantine.Put(ctx, PutRequest{
			RootPath: rootPath, OriginalPath: path.Join(rootPath, p.RelPath),
			RootFolderID: &rootFolderID, MediaObjectID: &mediaObjectID,
			Reason: ReasonFailedImport, Retain: retain,
		})
		if err != nil {
			if errors.Is(err, ErrCrossDevice) {
				// Already gone (deleted directly, Quarantine's own doc
				// comment) -- nothing to restore for this copy, but the
				// rest of the revert still proceeds.
				continue
			}
			return fmt.Errorf("quarantine %q: %w", p.RelPath, err)
		}
		result.QuarantinedPaths = append(result.QuarantinedPaths, entry)
	}
	return r.physical.DeleteObject(ctx, mediaObjectID)
}
