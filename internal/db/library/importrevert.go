package library

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"time"

	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// ErrSessionNotRevertible is only a session that finished inspecting has anything to undo; INSPECTING, IMPORTING and CANCELLING are undecided, REVERTED and CANCELLED are done.
var ErrSessionNotRevertible = errors.New("library: import session cannot be reverted from its current state")

// ErrRevertFileBlocked is a file the revert could not move to the recycle bin (a folder not writable, say); a retry may succeed.
var ErrRevertFileBlocked = errors.New("a file could not be moved to the recycle bin")

// Recycling is where a library's removed files go: how long recycle bin keeps them (Sonarr's
// recycling bin cleanup) and whether folders they leave empty are removed.
type Recycling struct {
	Retain             time.Duration
	RemoveEmptyFolders bool
}

// RevertResult is what one session's revert actually did.
type RevertResult struct {
	RecycledPaths []Entry
	// Restored are the files an upgrade had displaced that the revert brought back; NotRestored could not be (the path is
	// taken again, or the bin no longer has the file), and stay in the bin.
	Restored, NotRestored []Entry
	UnitsReverted         int
	WorksDeleted          int
	EditionsDeleted       int
	// SkippedSharedObjects are media_object ids revert left alone: bytes shared with a path from outside the session (ADR-11 dedupe) from before migration 0029, so it refuses to guess (Invariant 92).
	SkippedSharedObjects []int64
}

// ImportRevert is ADR-48's revert: recycle every placed file (section 10.8) and remove exactly the rows the session created (work_created/edition_created), composing stores as RemoveAndBlock does.
type ImportRevert struct {
	sessions   ImportSessions
	physical   Physical
	identity   Identity
	recycleBin RecycleBin
	rootPath   RootFolderPath
}

// NewImportRevert builds it over the stores it composes.
func NewImportRevert(
	sessions ImportSessions, physical Physical, identity Identity, recycleBin RecycleBin, rootPath RootFolderPath,
) ImportRevert {
	return ImportRevert{
		sessions: sessions, physical: physical, identity: identity, recycleBin: recycleBin, rootPath: rootPath,
	}
}

// Revert undoes sessionID's COMMIT placements; units never placed (WorkID nil) are left as they were. Two passes: recycle every file, then recount or delete work/edition rows and clear placement, so a partial revert is resumable; a work shared by two units is released from the second before deletion.
func (r ImportRevert) Revert(ctx context.Context, sessionID int64, recycling Recycling, at int64) (RevertResult, error) {
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
	unitExtras, err := r.sessions.UnitExtras(ctx, sessionID)
	if err != nil {
		return RevertResult{}, fmt.Errorf("library: revert session %d: %w", sessionID, err)
	}

	var result RevertResult
	var placed []Unit
	var firstFailure error
	fail := func(unit Unit, err error) {
		if firstFailure == nil {
			firstFailure = fmt.Errorf("library: revert unit %d: %w", unit.ID, err)
		}
	}
	// A unit that fails keeps its placement, which is what a retry finds it by; the others go on.
	for _, unit := range units {
		if unit.WorkID == nil {
			continue
		}
		if err := ctx.Err(); err != nil {
			return result, fmt.Errorf("library: revert session %d: %w", sessionID, err)
		}
		if err := r.recycleUnit(ctx, unit, unitFiles[unit.ID], unitExtras[unit.ID], recycling, &result); err != nil {
			fail(unit, err)
			continue
		}
		placed = append(placed, unit)
	}

	for _, unit := range placed {
		if err := r.closeUnit(ctx, unit, placed, &result); err != nil {
			fail(unit, err)
		}
	}
	if firstFailure != nil {
		return result, firstFailure
	}

	if err := r.sessions.Finish(ctx, sessionID, ImportReverted, at, ""); err != nil {
		return result, fmt.Errorf("library: revert session %d: mark reverted: %w", sessionID, err)
	}
	return result, nil
}

// recycleUnit moves a unit's files and extras into the recycle bin; safe to repeat, a file already gone is skipped.
func (r ImportRevert) recycleUnit(
	ctx context.Context, unit Unit, files []UnitFile, extras []UnitExtra, recycling Recycling, result *RevertResult,
) error {
	for _, file := range files {
		if err := r.recycleObject(ctx, file, recycling, result); err != nil {
			return err
		}
	}
	for _, extra := range extras {
		if err := r.recycleExtra(ctx, extra, recycling, result); err != nil {
			return err
		}
	}
	return r.sessions.DeleteUnitExtras(ctx, unit.ID)
}

// closeUnit recounts or deletes what the unit placed into and, last, forgets its placement: until then a retry still
// knows which work the unit made. Units of the session that point at a work or edition about to be deleted are
// released first (their own recount is done or tolerates the work being gone).
func (r ImportRevert) closeUnit(ctx context.Context, unit Unit, placed []Unit, result *RevertResult) error {
	if !unit.WorkCreated {
		if err := r.physical.RecomputeWorkItems(ctx, *unit.WorkID); err != nil {
			return err
		}
	}
	if !unit.EditionCreated && !unit.WorkCreated {
		if err := r.sessions.ClearUnitPlacement(ctx, unit.ID); err != nil {
			return fmt.Errorf("clear placement: %w", err)
		}
		result.UnitsReverted++
		return nil
	}
	release := []int64{unit.ID}
	for _, other := range placed {
		sharesWork := unit.WorkCreated && *other.WorkID == *unit.WorkID
		sharesEdition := unit.EditionCreated && unit.EditionID != nil && other.EditionID != nil && *other.EditionID == *unit.EditionID
		if other.ID != unit.ID && (sharesWork || sharesEdition) {
			release = append(release, other.ID)
		}
	}
	var editionID, workID *int64
	if unit.EditionCreated {
		editionID = unit.EditionID
	}
	if unit.WorkCreated {
		workID = unit.WorkID
	}
	if err := r.sessions.ReleaseAndDelete(ctx, release, editionID, workID); err != nil {
		return err
	}
	if editionID != nil {
		result.EditionsDeleted++
	}
	if workID != nil {
		result.WorksDeleted++
	}
	result.UnitsReverted++
	return nil
}

// recycleObject recycles only the library_path row this unit's placement created, deleting the media_object if it was the last path, because ADR-11 dedupe can point a placement at bytes an untouched commit owns; with no LibraryPathID (pre-0029) and several paths it refuses to guess (Invariant 92).
func (r ImportRevert) recycleObject(ctx context.Context, file UnitFile, recycling Recycling, result *RevertResult) error {
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
	entry, err := r.recycleBin.Put(ctx, PutRequest{
		RootPath: rootPath, OriginalPath: path.Join(rootPath, target.RelPath),
		RootFolderID: &rootFolderID, MediaObjectID: &mediaObjectID,
		Reason: ReasonFailedImport, Retain: recycling.Retain, RemoveEmptyFolders: recycling.RemoveEmptyFolders,
	})
	if err != nil {
		// A file deleted by hand since still has its rows go, or the revert never finishes.
		if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: %q: %w", ErrRevertFileBlocked, target.RelPath, err)
		}
	} else {
		result.RecycledPaths = append(result.RecycledPaths, entry)
	}

	r.restoreDisplaced(ctx, file.MediaObjectID, result)

	if len(paths) == 1 {
		return r.physical.DeleteObject(ctx, file.MediaObjectID)
	}
	return r.physical.DeleteLibraryPathRow(ctx, target.ID)
}

// restoreDisplaced brings back what the file being reverted had replaced, after the file itself is out of the way.
// Not being able to is reported, never an error: the revert is of the new file.
func (r ImportRevert) restoreDisplaced(ctx context.Context, mediaObjectID int64, result *RevertResult) {
	displaced, err := r.recycleBin.ReplacedBy(ctx, mediaObjectID)
	if err != nil {
		return
	}
	for _, entry := range displaced {
		if restored, err := r.recycleBin.Restore(ctx, entry.ID); err != nil {
			result.NotRestored = append(result.NotRestored, entry)
		} else {
			result.Restored = append(result.Restored, restored)
		}
	}
}

// recycleExtra moves one extra file the unit placed into the recycle bin; one already gone is skipped.
func (r ImportRevert) recycleExtra(ctx context.Context, extra UnitExtra, recycling Recycling, result *RevertResult) error {
	rootPath, ok := r.rootPath(extra.RootFolderID)
	if !ok {
		return fmt.Errorf("no known path for root folder %d", extra.RootFolderID)
	}
	rootFolderID := extra.RootFolderID
	entry, err := r.recycleBin.Put(ctx, PutRequest{
		RootPath: rootPath, OriginalPath: path.Join(rootPath, extra.RelPath), RootFolderID: &rootFolderID,
		Reason: ReasonFailedImport, Retain: recycling.Retain, RemoveEmptyFolders: recycling.RemoveEmptyFolders,
	})
	switch {
	case err == nil:
		result.RecycledPaths = append(result.RecycledPaths, entry)
	case errors.Is(err, fs.ErrNotExist):
	default:
		return fmt.Errorf("recycle bin extra %q: %w", extra.RelPath, err)
	}
	return nil
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
