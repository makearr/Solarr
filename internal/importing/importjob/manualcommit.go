package importjob

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"

	"github.com/makearr/MediaNexus/internal/db/library"
	"github.com/makearr/MediaNexus/internal/identify/model"
	"github.com/makearr/MediaNexus/internal/importing/inspect"
	"github.com/makearr/MediaNexus/internal/media/music"
	"github.com/makearr/MediaNexus/internal/platform/fsx"
	"github.com/makearr/MediaNexus/internal/platform/mediatool"
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
// (commitCluster's own Walk call passes an empty WalkOptions{}) -- except
// when a unit shares its own rel_path with sibling units (a flat directory
// GROUP split into several by track continuity alone), which this refuses
// rather than guessing (ErrUnitSharesDirectory, a known, narrow gap).
type ManualCommitter struct {
	fsys      fsx.FS
	tool      mediatool.Tool
	sessions  library.ImportSessions
	committer Committer
}

// NewManualCommitter builds one. All four are required.
func NewManualCommitter(fsys fsx.FS, tool mediatool.Tool, sessions library.ImportSessions, committer Committer) ManualCommitter {
	return ManualCommitter{fsys: fsys, tool: tool, sessions: sessions, committer: committer}
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

// ErrUnitSharesDirectory means this unit's own rel_path is not unique within
// its session -- GROUP's track-continuity fallback can split one flat
// directory (several "albums" dropped together with no subfolders) into
// several sibling units that all report the same Directory (inspect.Unit's
// own doc comment). import_unit persists no member-file list, only the
// shared rel_path, so a re-walk of that one path reconstructs every sibling
// at once with no reliable way to say which one this unit was -- refused
// rather than guessing and risking placing a different sibling's files under
// this unit's own identity. Found live-verifying todo 4.9 against a real
// flat scene dump (private/questions.md); re-deriving through the exact
// Walker.Walk cursor sequence that produced the original split, instead of a
// single inspect.Run, would resolve it -- not attempted this pass.
var ErrUnitSharesDirectory = errors.New("importjob: this unit shares its directory with other units; commit or place it during import instead")

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

// refuseIfSharesDirectory reports ErrUnitSharesDirectory when another unit
// in the same session shares this one's own rel_path -- checked up front,
// before the re-walk that would otherwise fail with the far less honest
// ErrUnitChanged (that error means "the source changed"; this one means "the
// source was always ambiguous, re-deriving it by directory alone cannot
// tell these apart").
func (m ManualCommitter) refuseIfSharesDirectory(ctx context.Context, unit library.Unit) error {
	siblings, err := m.sessions.ListUnits(ctx, unit.SessionID)
	if err != nil {
		return fmt.Errorf("importjob: list units for session %d: %w", unit.SessionID, err)
	}
	for _, sibling := range siblings {
		if sibling.ID != unit.ID && sibling.RelPath == unit.RelPath {
			return fmt.Errorf("importjob: unit %d: %w", unit.ID, ErrUnitSharesDirectory)
		}
	}
	return nil
}

// place re-derives one unit's own file list and tags by re-walking exactly
// its already-decided boundary (unit.RelPath under the session's own
// source_path), then runs the same CommitUnit WALK-time placement already
// uses. A file-count mismatch after the re-walk means the source changed
// since inspection -- refused rather than placing a different set of files
// than what IDENTIFY actually ranked.
func (m ManualCommitter) place(
	ctx context.Context, session library.Session, unit library.Unit, best model.Identification,
) (music.Placed, error) {
	if err := m.refuseIfSharesDirectory(ctx, unit); err != nil {
		return music.Placed{}, err
	}

	root := path.Join(session.SourcePath, unit.RelPath)
	found, err := inspect.Run(ctx, m.fsys, root, inspect.Options{})
	if err != nil {
		return music.Placed{}, fmt.Errorf("importjob: re-walk unit %d: %w", unit.ID, err)
	}
	if len(found.Units) != 1 {
		return music.Placed{}, fmt.Errorf("importjob: unit %d: re-walk found %d unit(s), expected exactly 1: %w",
			unit.ID, len(found.Units), ErrUnitChanged)
	}
	reconstructed := found.Units[0]
	if int64(len(reconstructed.Media)) != unit.FileCount {
		return music.Placed{}, fmt.Errorf("importjob: unit %d: re-walk found %d file(s), expected %d: %w",
			unit.ID, len(reconstructed.Media), unit.FileCount, ErrUnitChanged)
	}
	// reconstructed.Directory is "" (re-walked at its own root) -- restore
	// the real rel_path, which CommitUnit's own error messages and its
	// directory-name naming fallback both read.
	reconstructed.Directory = unit.RelPath

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

	placed, err := m.committer.CommitUnit(ctx, root, reconstructed, best, tags, *session.CategoryID)
	if err != nil {
		return music.Placed{}, fmt.Errorf("importjob: commit unit %d: %w", unit.ID, err)
	}
	if err := m.sessions.RecordPlacement(ctx, unit.ID, library.Placement{
		WorkID: placed.WorkID, WorkCreated: placed.WorkCreated,
		EditionID: placed.EditionID, EditionCreated: placed.EditionCreated,
		MediaObjectIDs: placed.MediaObjectIDs,
	}); err != nil {
		return music.Placed{}, fmt.Errorf("importjob: record placement for unit %d: %w", unit.ID, err)
	}
	return placed, nil
}
