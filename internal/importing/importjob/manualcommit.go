package importjob

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/model"
	importbatch "github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// ManualCommitter is what a person does with a session's units the walk did not place: commit one it held (as its
// best guess, or as a work named), commit every held one, and place what was decided before the session had a
// library. import_unit keeps no file list, so each re-walks the unit's own folder (reconstructUnit) and hands it to
// its library's media type, which places it as that type does.
type ManualCommitter struct {
	fsys     fsx.FS
	sessions library.ImportSessions
	// packs moves a placed unit's pack membership from QUEUED to RESOLVED, as the walk's placement does.
	packs library.Packs
	// importerFor is the importer of a library's media type, nil for a type without one.
	importerFor func(categoryID int64) importbatch.Importer
}

// ErrHeldCommitNotBuilt is a library whose media type cannot place a held unit by hand.
var ErrHeldCommitNotBuilt = errors.New("importjob: this library's media type does not commit held units by hand")

// NewManualCommitter builds one; every argument is required.
func NewManualCommitter(
	fsys fsx.FS, sessions library.ImportSessions, packs library.Packs, importerFor func(categoryID int64) importbatch.Importer,
) ManualCommitter {
	return ManualCommitter{fsys: fsys, sessions: sessions, packs: packs, importerFor: importerFor}
}

// ErrSessionHasNoCategory means the session must be given a category
// (AssignCategory) before anything of its own can be placed.
var ErrSessionHasNoCategory = errors.New("importjob: import session has no category to place into")

// ErrSessionReverted means every placement this session made has already
// been undone -- refused rather than silently re-creating what a revert just
// removed.
var ErrSessionReverted = errors.New("importjob: import session was reverted")

// ErrUnitNotQueued means CommitHeld was asked to override a unit that is not
// currently QUEUE -- only a below-threshold unit has a held decision to
// override.
var ErrUnitNotQueued = errors.New("importjob: unit is not queued for review")

// ErrNoCandidate means a unit has nothing IDENTIFY ever ranked to commit --
// true of a FAILED unit and of a QUEUE unit GROUP alone could not resolve at
// all.
var ErrNoCandidate = errors.New("importjob: unit has no candidate to commit")

// ErrUnitChanged means a re-walk of the unit's own directory no longer
// matches what WALK originally recorded (a different file count) -- the
// source changed since inspection, and re-deriving a stale unit's placement
// would silently commit the wrong thing.
var ErrUnitChanged = errors.New("importjob: unit's own files no longer match what was inspected")

// CommitHeld force-commits one QUEUE-decision unit as its own best guess: a person overriding DECIDE's threshold,
// not a second identification.
func (m ManualCommitter) CommitHeld(ctx context.Context, unitID int64) (library.Placement, error) {
	return m.commitHeld(ctx, unitID, nil)
}

// CommitHeldAs force-commits one QUEUE-decision unit as the work a person named ("Identify as...", for a unit
// IDENTIFY found nothing for, or found the wrong thing): a stated identifier outranks any guess.
func (m ManualCommitter) CommitHeldAs(ctx context.Context, unitID int64, provider, externalID string) (library.Placement, error) {
	return m.commitHeld(ctx, unitID, &importbatch.Named{Provider: provider, ExternalID: externalID})
}

func (m ManualCommitter) commitHeld(ctx context.Context, unitID int64, named *importbatch.Named) (library.Placement, error) {
	unit, err := m.sessions.GetUnit(ctx, unitID)
	if err != nil {
		return library.Placement{}, fmt.Errorf("importjob: get unit %d: %w", unitID, err)
	}
	if unit.Decision != library.UnitQueue {
		return library.Placement{}, fmt.Errorf("importjob: unit %d: %w", unitID, ErrUnitNotQueued)
	}
	session, err := m.sessions.Get(ctx, unit.SessionID)
	if err != nil {
		return library.Placement{}, fmt.Errorf("importjob: get session %d: %w", unit.SessionID, err)
	}
	if err := checkPlaceable(session); err != nil {
		return library.Placement{}, err
	}
	return m.placeHeld(ctx, session, unit, named)
}

// minimumGuess keeps a guess of no confidence at all distinguishable from a person's naming.
const minimumGuess = 0.001

// placeHeld hands one unit to its library's media type, re-walked from its own folder, and records what was placed.
func (m ManualCommitter) placeHeld(
	ctx context.Context, session library.Session, unit library.Unit, named *importbatch.Named,
) (library.Placement, error) {
	importer := m.importerFor(*session.CategoryID)
	committer, ok := importer.(importbatch.HeldCommitter)
	if !ok {
		return library.Placement{}, fmt.Errorf("importjob: unit %d: %w", unit.ID, ErrHeldCommitNotBuilt)
	}
	var floor int64
	if sized, ok := importer.(importbatch.SizeFloor); ok {
		floor = sized.MediaSizeFloorBytes()
	}
	reconstructed, err := m.reconstructUnit(ctx, path.Join(session.SourcePath, unit.RelPath), unit, floor)
	if err != nil {
		return library.Placement{}, err
	}
	// A batch's files are named from the session's source, a re-walk's from the unit's own folder.
	for index := range reconstructed.Media {
		reconstructed.Media[index].RelPath = path.Join(unit.RelPath, reconstructed.Media[index].RelPath)
	}
	for index := range reconstructed.Other {
		reconstructed.Other[index].RelPath = path.Join(unit.RelPath, reconstructed.Other[index].RelPath)
	}
	request := importbatch.HeldRequest{
		SessionID: session.ID, CategoryID: *session.CategoryID, AcquisitionID: session.AcquisitionID,
		SourcePath: session.SourcePath, Unit: reconstructed, As: named,
	}
	if top, err := topCandidate(unit); err == nil {
		request.Top = &top
	}
	if named == nil && unit.WorkID == nil && request.Top == nil {
		return library.Placement{}, fmt.Errorf("importjob: unit %d: %w", unit.ID, ErrNoCandidate)
	}
	switch {
	case unit.WorkID != nil:
		request.WorkID = *unit.WorkID
	case named == nil && request.Top.Strategy == importbatch.StrategyResolved && request.Top.WorkID != nil:
		request.WorkID = *request.Top.WorkID
	case named == nil && request.Top.ExternalID != nil:
		// Commit anyway means the best guess for every type: here the top-ranked candidate's own id.
		request.As = &importbatch.Named{Provider: request.Top.ExternalID.Provider, ExternalID: request.Top.ExternalID.ExternalID}
		request.Guess = max(request.Top.Confidence, minimumGuess)
	}
	placement, err := committer.CommitHeld(ctx, request)
	if err != nil {
		return library.Placement{}, fmt.Errorf("importjob: commit unit %d: %w", unit.ID, err)
	}
	// A held unit records the override; a retried one drops why its library could not take it.
	if unit.Decision == library.UnitQueue || unit.Reason != "" {
		reason := "manual override"
		if unit.Decision == library.UnitCommit {
			reason = ""
		}
		if err := m.sessions.SetUnitDecisions(ctx, session.ID, []library.UnitDecision{{
			UnitID: unit.ID, Decision: library.UnitCommit, Confidence: 1, WorkID: &placement.WorkID,
			CandidatesJSON: unit.CandidatesJSON, Reason: reason,
		}}); err != nil {
			return library.Placement{}, fmt.Errorf("importjob: record decision override for unit %d: %w", unit.ID, err)
		}
	}
	if err := m.sessions.RecordPlacement(ctx, unit.ID, placement); err != nil {
		return library.Placement{}, fmt.Errorf("importjob: record placement for unit %d: %w", unit.ID, err)
	}
	if unit.PackID != nil {
		if err := m.packs.ResolveItem(ctx, unit.ID, placement.WorkID, placement.EditionID); err != nil {
			return library.Placement{}, fmt.Errorf("importjob: resolve pack item for unit %d: %w", unit.ID, err)
		}
	}
	return placement, nil
}

// PlaceUnplaced places every unit this session decided COMMIT but never placed, which is what a session walked before
// it had a library holds once it is given one. Best-effort: one bad unit does not stop the rest.
func (m ManualCommitter) PlaceUnplaced(ctx context.Context, sessionID int64) (placed int, errs []error) {
	session, err := m.sessions.Get(ctx, sessionID)
	if err != nil {
		return 0, []error{fmt.Errorf("importjob: get session %d: %w", sessionID, err)}
	}
	if err := checkPlaceable(session); err != nil {
		return 0, []error{err}
	}
	units, err := m.sessions.ListUnplacedCommitUnits(ctx, sessionID)
	if err != nil {
		return 0, []error{fmt.Errorf("importjob: list unplaced units for session %d: %w", sessionID, err)}
	}
	for _, unit := range units {
		if _, err := topCandidate(unit); err != nil {
			errs = append(errs, err)
			continue
		}
		if _, err := m.placeHeld(ctx, session, unit, nil); err != nil {
			errs = append(errs, err)
			if closeErr := m.closeUnplaceable(ctx, session.ID, unit, err); closeErr != nil {
				errs = append(errs, closeErr)
			}
			continue
		}
		placed++
	}
	return placed, errs
}

// closeUnplaceable ends a unit no later try can place, so its download is failed rather than kept for ever: nothing
// in it is new any more, or its files are gone. A library that still cannot take it leaves it waiting.
func (m ManualCommitter) closeUnplaceable(ctx context.Context, sessionID int64, unit library.Unit, placeErr error) error {
	decision := library.UnitDecision{UnitID: unit.ID, CandidatesJSON: unit.CandidatesJSON, Reason: placeErr.Error()}
	switch {
	case errors.Is(placeErr, importbatch.ErrNothingToPlace):
		decision.Decision, decision.Reason = library.UnitSkip, importbatch.ReasonNothingNew
	case errors.Is(placeErr, ErrUnitChanged):
		decision.Decision = library.UnitFailed
	default:
		return nil
	}
	if err := m.sessions.SetUnitDecisions(ctx, sessionID, []library.UnitDecision{decision}); err != nil {
		return fmt.Errorf("importjob: close unit %d: %w", unit.ID, err)
	}
	return nil
}

// CommitAllHeld force-commits the units a preview run held for review (revision.md 7.9's "commit all / selected"):
// CommitHeld repeated. only, when non-empty, names the subset; best-effort as PlaceUnplaced is.
func (m ManualCommitter) CommitAllHeld(ctx context.Context, sessionID int64, only []int64) (committed int, errs []error) {
	session, err := m.sessions.Get(ctx, sessionID)
	if err != nil {
		return 0, []error{fmt.Errorf("importjob: get session %d: %w", sessionID, err)}
	}
	if err := checkPlaceable(session); err != nil {
		return 0, []error{err}
	}
	units, err := m.sessions.ListUnits(ctx, sessionID)
	if err != nil {
		return 0, []error{fmt.Errorf("importjob: list units for session %d: %w", sessionID, err)}
	}
	for _, unit := range units {
		if unit.Decision != library.UnitQueue || (len(only) > 0 && !slices.Contains(only, unit.ID)) {
			continue
		}
		if _, err := m.placeHeld(ctx, session, unit, nil); err != nil {
			errs = append(errs, err)
			continue
		}
		committed++
	}
	return committed, errs
}

// checkPlaceable guards both entry points against the two states that must
// never gain a new placement: no destination category yet, and a session
// whose own placements were already undone (ClearImportUnitPlacement does
// not touch import_unit.decision, so a reverted session's own COMMIT units
// would otherwise look "unplaced" again to ListUnplacedCommitUnits).
func checkPlaceable(session library.Session) error {
	if session.State == library.ImportReverted {
		return fmt.Errorf("importjob: session %d: %w", session.ID, ErrSessionReverted)
	}
	if session.CategoryID == nil {
		return fmt.Errorf("importjob: session %d: %w", session.ID, ErrSessionHasNoCategory)
	}
	return nil
}

// topCandidate decodes a unit's own ranked candidates (IDENTIFY's merged
// result, best first -- model.ChainResult.Candidates' own doc comment) and
// answers the first.
func topCandidate(unit library.Unit) (model.Identification, error) {
	if unit.CandidatesJSON == "" {
		return model.Identification{}, fmt.Errorf("importjob: unit %d: %w", unit.ID, ErrNoCandidate)
	}
	candidates, err := library.UnitCandidates(unit.CandidatesJSON)
	if err != nil {
		return model.Identification{}, fmt.Errorf("importjob: decode candidates for unit %d: %w", unit.ID, err)
	}
	if len(candidates) == 0 {
		return model.Identification{}, fmt.Errorf("importjob: unit %d: %w", unit.ID, ErrNoCandidate)
	}
	return candidates[0], nil
}

// reconstructUnit re-derives one unit's own file list by re-walking exactly
// its already-decided boundary (unit.RelPath under the session's own
// source_path). The overwhelming common case -- one import_unit per
// directory -- uses a plain inspect.Run. Several import_unit rows sharing
// one rel_path (GROUP's track-continuity fallback splitting a flat
// multi-album drop into siblings that all report the same Directory,
// inspect.Unit's own doc comment) instead re-derives through
// inspect.SplitDirectory -- Walker's own per-cluster split, in Walker's own
// unsorted emission order -- and matches each sibling back to its own
// import_unit row by position in ascending id order: the same order
// AddUnits inserted every sibling from one WALK cluster in, which is the
// same order Walker's own batch (one emit call per directory) arrived in.
// inspect.Run's own group() is not equivalent for this: it re-sorts every
// unit by (directory, firstPath) before returning, a different order than
// Walker ever emitted a multi-unit cluster in. Either path ends the same
// way: a file-count mismatch means the source changed since inspection,
// refused rather than placing a different set of files than IDENTIFY
// actually ranked.
func (m ManualCommitter) reconstructUnit(ctx context.Context, root string, unit library.Unit, floorBytes int64) (inspect.Unit, error) {
	siblings, err := m.sessions.ListUnits(ctx, unit.SessionID)
	if err != nil {
		return inspect.Unit{}, fmt.Errorf("importjob: list units for session %d: %w", unit.SessionID, err)
	}
	var sharing []library.Unit
	for _, sibling := range siblings {
		if sibling.RelPath == unit.RelPath {
			sharing = append(sharing, sibling)
		}
	}

	var reconstructed inspect.Unit
	if len(sharing) <= 1 {
		found, err := inspect.Run(ctx, m.fsys, root, inspect.Options{MediaSizeFloorBytes: floorBytes})
		if errors.Is(err, fs.ErrNotExist) {
			return inspect.Unit{}, fmt.Errorf("importjob: unit %d: its folder is gone: %w", unit.ID, ErrUnitChanged)
		}
		if err != nil {
			return inspect.Unit{}, fmt.Errorf("importjob: re-walk unit %d: %w", unit.ID, err)
		}
		if len(found.Units) != 1 {
			return inspect.Unit{}, fmt.Errorf("importjob: unit %d: re-walk found %d unit(s), expected exactly 1: %w",
				unit.ID, len(found.Units), ErrUnitChanged)
		}
		reconstructed = found.Units[0]
	} else {
		units, err := inspect.SplitDirectory(m.fsys, root, nil, floorBytes)
		if err != nil {
			return inspect.Unit{}, fmt.Errorf("importjob: re-split directory for unit %d: %w", unit.ID, err)
		}
		if len(units) != len(sharing) {
			return inspect.Unit{}, fmt.Errorf(
				"importjob: unit %d: re-split found %d unit(s) sharing %q, expected %d: %w",
				unit.ID, len(units), unit.RelPath, len(sharing), ErrUnitChanged)
		}
		index := slices.IndexFunc(sharing, func(u library.Unit) bool { return u.ID == unit.ID })
		if index < 0 {
			return inspect.Unit{}, fmt.Errorf("importjob: unit %d: not found among its own siblings sharing %q",
				unit.ID, unit.RelPath)
		}
		reconstructed = units[index]
	}

	if int64(len(reconstructed.Media)) != unit.FileCount {
		return inspect.Unit{}, fmt.Errorf("importjob: unit %d: re-walk found %d file(s), expected %d: %w",
			unit.ID, len(reconstructed.Media), unit.FileCount, ErrUnitChanged)
	}
	// reconstructed.Directory is "" (re-walked/re-split at its own root) --
	// restore the real rel_path, which CommitUnit's own error messages and
	// its directory-name naming fallback both read.
	reconstructed.Directory = unit.RelPath
	return reconstructed, nil
}
