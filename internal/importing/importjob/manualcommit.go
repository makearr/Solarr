package importjob

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// ManualCommitter offers the two todo-4.9 UI actions the inline WALK-time
// commit (commitCluster/placeCommitted) does not cover: force-committing one
// unit the pipeline queued below threshold ("commit what was held"), and
// placing whatever a session already decided COMMIT once it is given a
// category it did not have while walking (a session can start with no
// category and get one later, questions.md). import_unit persists no
// member-file list of its own (notes.md §5), so both re-derive it by
// re-walking the unit's own already-decided boundary (rel_path) -- the same
// "re-derive rather than duplicate state" rule moverjob's own resume already
// follows. Deterministic today since WALK/GROUP runs tag-free in production
// (commitCluster's own Walk call passes an empty WalkOptions{}). A unit
// sharing its own rel_path with sibling units (a flat directory GROUP split
// into several by track continuity alone) re-derives through
// inspect.SplitDirectory -- Walker's own per-cluster split, unsorted -- and
// matches each sibling back to its own import_unit row by position in
// ascending id order, the same order AddUnits inserted them in and Walker's
// own batch arrived in (reconstructUnit).
type ManualCommitter struct {
	fsys      fsx.FS
	tool      mediatool.Tool
	sessions  library.ImportSessions
	committer Committer
	// packs is todo 4.11's own pack_item state keeper: a unit resolved
	// through this package moves its pack membership (if any) from QUEUED to
	// RESOLVED too, the same way commitCluster's own inline placement does
	// (job.go). Safe to leave zero-valued in a caller that never places a
	// unit with a non-nil PackID (every existing test but the pack-specific
	// ones), since place() checks unit.PackID first.
	packs library.Packs
}

// NewManualCommitter builds one. packs may be a zero-valued library.Packs in
// a caller that never places a unit with a non-nil PackID (place() checks
// PackID first); every other argument is required.
func NewManualCommitter(
	fsys fsx.FS, tool mediatool.Tool, sessions library.ImportSessions, committer Committer, packs library.Packs,
) ManualCommitter {
	return ManualCommitter{fsys: fsys, tool: tool, sessions: sessions, committer: committer, packs: packs}
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

// CommitHeld force-commits one QUEUE-decision unit using its own top-ranked
// candidate -- a human override of DECIDE's own threshold, not a second
// identification: the same candidate IDENTIFY already ranked, never re-run.
func (m ManualCommitter) CommitHeld(ctx context.Context, unitID int64) (music.Placed, error) {
	unit, err := m.sessions.GetUnit(ctx, unitID)
	if err != nil {
		return music.Placed{}, fmt.Errorf("importjob: get unit %d: %w", unitID, err)
	}
	if unit.Decision != library.UnitQueue {
		return music.Placed{}, fmt.Errorf("importjob: unit %d: %w", unitID, ErrUnitNotQueued)
	}
	session, err := m.sessions.Get(ctx, unit.SessionID)
	if err != nil {
		return music.Placed{}, fmt.Errorf("importjob: get session %d: %w", unit.SessionID, err)
	}
	if err := checkPlaceable(session); err != nil {
		return music.Placed{}, err
	}

	best, err := topCandidate(unit)
	if err != nil {
		return music.Placed{}, err
	}
	return m.place(ctx, session, unit, best)
}

// PlaceUnplaced places every unit this session already decided COMMIT but
// never placed -- todo 4.9's other UI action: assigning a category to a
// session after WALK already decided some units. Best-effort per unit, the
// same tolerance placeCommitted already has inline during WALK: one bad unit
// does not stop the rest.
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
		best, err := topCandidate(unit)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if _, err := m.place(ctx, session, unit, best); err != nil {
			errs = append(errs, err)
			continue
		}
		placed++
	}
	return placed, errs
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
	var candidates []model.Identification
	if err := json.Unmarshal([]byte(unit.CandidatesJSON), &candidates); err != nil {
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
func (m ManualCommitter) reconstructUnit(ctx context.Context, root string, unit library.Unit) (inspect.Unit, error) {
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
		found, err := inspect.Run(ctx, m.fsys, root, inspect.Options{})
		if err != nil {
			return inspect.Unit{}, fmt.Errorf("importjob: re-walk unit %d: %w", unit.ID, err)
		}
		if len(found.Units) != 1 {
			return inspect.Unit{}, fmt.Errorf("importjob: unit %d: re-walk found %d unit(s), expected exactly 1: %w",
				unit.ID, len(found.Units), ErrUnitChanged)
		}
		reconstructed = found.Units[0]
	} else {
		units, err := inspect.SplitDirectory(m.fsys, root, nil, 0)
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

// place runs the same CommitUnit WALK-time placement already uses, over a
// unit reconstructUnit re-derived.
func (m ManualCommitter) place(
	ctx context.Context, session library.Session, unit library.Unit, best model.Identification,
) (music.Placed, error) {
	root := path.Join(session.SourcePath, unit.RelPath)
	reconstructed, err := m.reconstructUnit(ctx, root, unit)
	if err != nil {
		return music.Placed{}, err
	}

	absolute := make([]string, len(reconstructed.Media))
	for i, f := range reconstructed.Media {
		absolute[i] = path.Join(root, f.RelPath)
	}
	probes, err := m.tool.ReadTags(ctx, absolute)
	if err != nil {
		return music.Placed{}, fmt.Errorf("importjob: probe unit %d: %w", unit.ID, err)
	}
	tags := music.UnitTagSourceFor(root, probes)

	// DECIDE's own contract (identifyUnit): work_id/edition_id stay nil until
	// RecordPlacement fills them in below -- flipping decision here first,
	// same order commitCluster's own SetUnitDecisions-then-placeCommitted
	// already uses, so a placement failure leaves the unit exactly where a
	// WALK-time placement failure already would: COMMIT-decided, unplaced,
	// picked up again by a later PlaceUnplaced retry.
	if unit.Decision == library.UnitQueue {
		if err := m.sessions.SetUnitDecisions(ctx, session.ID, []library.UnitDecision{{
			UnitID: unit.ID, Decision: library.UnitCommit, Confidence: best.Confidence,
			CandidatesJSON: unit.CandidatesJSON, Reason: "manual override",
		}}); err != nil {
			return music.Placed{}, fmt.Errorf("importjob: record decision override for unit %d: %w", unit.ID, err)
		}
	}

	// No album placement: a manual commit re-reads the unit from disk, where what album
	// matching decided during IDENTIFY is no longer in memory (the candidates are, in the row).
	placed, err := m.committer.CommitMatchedUnit(ctx, root, reconstructed, best, tags, *session.CategoryID, nil)
	if err != nil {
		return music.Placed{}, fmt.Errorf("importjob: commit unit %d: %w", unit.ID, err)
	}
	if err := m.sessions.RecordPlacement(ctx, unit.ID, library.Placement{
		WorkID: placed.WorkID, WorkCreated: placed.WorkCreated,
		EditionID: placed.EditionID, EditionCreated: placed.EditionCreated,
		MediaObjectIDs: placed.MediaObjectIDs, LibraryPathIDs: placed.LibraryPathIDs,
	}); err != nil {
		return music.Placed{}, fmt.Errorf("importjob: record placement for unit %d: %w", unit.ID, err)
	}
	if unit.PackID != nil {
		if err := m.packs.ResolveItem(ctx, unit.ID, placed.WorkID, placed.EditionID); err != nil {
			return music.Placed{}, fmt.Errorf("importjob: resolve pack item for unit %d: %w", unit.ID, err)
		}
	}
	return placed, nil
}
