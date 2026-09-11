package importjob_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/makearr/MediaNexus/internal/db/library"
	"github.com/makearr/MediaNexus/internal/identify/model"
	"github.com/makearr/MediaNexus/internal/importing/importjob"
	"github.com/makearr/MediaNexus/internal/importing/inspect"
	"github.com/makearr/MediaNexus/internal/media/music"
	"github.com/makearr/MediaNexus/internal/platform/mediatool"
)

// fakeCommitter is importjob.Committer's own interface, faked -- ManualCommitter
// never needs a real placement.Resolve/library.Identity chain to verify its
// own re-derivation and error handling. workID must name a real work row
// (RecordPlacement's own import_unit.work_id carries a real FK), seeded by
// seedWork.
type fakeCommitter struct {
	workID int64
	// placed, keyed by sourceRoot, overrides workID for one call's own answer.
	// errFor, keyed the same way, makes one call fail instead.
	placed map[string]music.Placed
	errFor map[string]error

	calls []fakeCommitterCall
}

type fakeCommitterCall struct {
	sourceRoot string
	directory  string
	fileCount  int
	externalID string
	categoryID int64
}

func (f *fakeCommitter) CommitUnit(
	_ context.Context, sourceRoot string, unit inspect.Unit, best model.Identification, _ music.Resolved, categoryID int64,
) (music.Placed, error) {
	key := sourceRoot
	f.calls = append(f.calls, fakeCommitterCall{
		sourceRoot: sourceRoot, directory: unit.Directory, fileCount: len(unit.Media),
		externalID: best.ExternalID.ExternalID, categoryID: categoryID,
	})
	if err, ok := f.errFor[key]; ok {
		return music.Placed{}, err
	}
	if placed, ok := f.placed[key]; ok {
		return placed, nil
	}
	return music.Placed{WorkID: f.workID}, nil
}

// seedWork creates one real work row, for a fakeCommitter's own Placed.WorkID
// to name -- import_unit.work_id carries a real foreign key, so a test that
// exercises RecordPlacement needs a row that actually exists.
func seedWork(t *testing.T, fx *fixture) int64 {
	t.Helper()
	identity := library.NewIdentity(fx.store)
	resolutions, err := identity.ResolveWorksDetailed(t.Context(), fx.clock.Now(), []library.WorkLink{{
		Provider: "test", ExternalID: t.Name(),
		Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: t.Name(), SortTitle: t.Name()},
	}})
	if err != nil {
		t.Fatalf("seedWork: %v", err)
	}
	return resolutions[0].WorkID
}

// A unit DECIDE queued below threshold (a real release-group MBID at 0.95,
// against a 0.97 threshold) still carries that candidate -- CommitHeld places
// it using exactly that candidate, without re-running IDENTIFY.
func TestManualCommitter_CommitHeldPlacesAQueuedUnitUsingItsTopCandidate(t *testing.T) {
	fx := newFixture(t, "Artist/Album/01.flac", "Artist/Album/02.flac")
	fx.tool.byPath[filepath.Join(root, "Artist/Album/01.flac")] =
		mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID}
	fx.tool.byPath[filepath.Join(root, "Artist/Album/02.flac")] =
		mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID}

	catID := int64(7)
	job := fx.job(t, 0.97) // above confidenceReleaseGroup (0.95): queues, not commits
	if err := fx.run(t, job, importjob.Payload{
		SourcePath: root, Origin: library.ImportOriginManual, CategoryID: &catID,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	sessions := fx.sessions()
	done, err := sessions.List(t.Context(), library.ImportDone)
	if err != nil || len(done) != 1 {
		t.Fatalf("List: %v, %+v", err, done)
	}
	units, err := sessions.ListUnits(t.Context(), done[0].ID)
	if err != nil || len(units) != 1 || units[0].Decision != library.UnitQueue {
		t.Fatalf("units = %+v, %v, want one QUEUE unit", units, err)
	}

	workID := seedWork(t, fx)
	committer := &fakeCommitter{workID: workID}
	manual := importjob.NewManualCommitter(fx.fsys, fx.tool, sessions, committer)
	placed, err := manual.CommitHeld(t.Context(), units[0].ID)
	if err != nil {
		t.Fatalf("CommitHeld: %v", err)
	}
	if placed.WorkID != workID {
		t.Errorf("WorkID = %d, want %d", placed.WorkID, workID)
	}
	if len(committer.calls) != 1 {
		t.Fatalf("committer was called %d times, want 1", len(committer.calls))
	}
	call := committer.calls[0]
	if call.fileCount != 2 || call.externalID != releaseGroupMBID || call.categoryID != catID {
		t.Errorf("call = %+v, want fileCount=2 externalID=%s categoryID=%d", call, releaseGroupMBID, catID)
	}

	updated, err := sessions.GetUnit(t.Context(), units[0].ID)
	if err != nil {
		t.Fatalf("GetUnit: %v", err)
	}
	if updated.Decision != library.UnitCommit {
		t.Errorf("Decision = %q, want COMMIT after the override", updated.Decision)
	}
	if updated.WorkID == nil || *updated.WorkID != workID {
		t.Errorf("WorkID = %v, want %d", updated.WorkID, workID)
	}
}

func TestManualCommitter_CommitHeldRefusesAUnitThatIsNotQueued(t *testing.T) {
	fx := newFixture(t, "Artist/Album/01.flac", "Artist/Album/02.flac")
	fx.tool.byPath[filepath.Join(root, "Artist/Album/01.flac")] = mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID}
	fx.tool.byPath[filepath.Join(root, "Artist/Album/02.flac")] = mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID}

	catID := int64(7)
	job := fx.job(t, 0.90) // clears the default threshold: COMMIT, not QUEUE
	if err := fx.run(t, job, importjob.Payload{
		SourcePath: root, Origin: library.ImportOriginManual, CategoryID: &catID,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	sessions := fx.sessions()
	done, _ := sessions.List(t.Context(), library.ImportDone)
	units, _ := sessions.ListUnits(t.Context(), done[0].ID)
	if units[0].Decision != library.UnitCommit {
		t.Fatalf("unit decision = %q, want COMMIT (test setup)", units[0].Decision)
	}

	manual := importjob.NewManualCommitter(fx.fsys, fx.tool, sessions, &fakeCommitter{})
	_, err := manual.CommitHeld(t.Context(), units[0].ID)
	if !errors.Is(err, importjob.ErrUnitNotQueued) {
		t.Errorf("err = %v, want ErrUnitNotQueued", err)
	}
}

func TestManualCommitter_CommitHeldRefusesAUnitWithNoCandidate(t *testing.T) {
	fx := newFixture(t, "Artist/Album/01.flac") // no tags at all: IDENTIFY finds nothing
	catID := int64(7)
	job := fx.job(t, 0.90)
	if err := fx.run(t, job, importjob.Payload{
		SourcePath: root, Origin: library.ImportOriginManual, CategoryID: &catID,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	sessions := fx.sessions()
	done, _ := sessions.List(t.Context(), library.ImportDone)
	units, _ := sessions.ListUnits(t.Context(), done[0].ID)
	if units[0].Decision != library.UnitQueue || units[0].CandidatesJSON != "" {
		t.Fatalf("unit = %+v, want QUEUE with no candidates (test setup)", units[0])
	}

	manual := importjob.NewManualCommitter(fx.fsys, fx.tool, sessions, &fakeCommitter{})
	_, err := manual.CommitHeld(t.Context(), units[0].ID)
	if !errors.Is(err, importjob.ErrNoCandidate) {
		t.Errorf("err = %v, want ErrNoCandidate", err)
	}
}

func TestManualCommitter_CommitHeldRefusesWithNoSessionCategory(t *testing.T) {
	fx := newFixture(t, "Artist/Album/01.flac", "Artist/Album/02.flac")
	fx.tool.byPath[filepath.Join(root, "Artist/Album/01.flac")] = mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID}
	fx.tool.byPath[filepath.Join(root, "Artist/Album/02.flac")] = mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID}

	job := fx.job(t, 0.97) // queues; no CategoryID on the payload
	if err := fx.run(t, job, importjob.Payload{SourcePath: root, Origin: library.ImportOriginManual}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	sessions := fx.sessions()
	done, _ := sessions.List(t.Context(), library.ImportDone)
	units, _ := sessions.ListUnits(t.Context(), done[0].ID)

	manual := importjob.NewManualCommitter(fx.fsys, fx.tool, sessions, &fakeCommitter{})
	_, err := manual.CommitHeld(t.Context(), units[0].ID)
	if !errors.Is(err, importjob.ErrSessionHasNoCategory) {
		t.Errorf("err = %v, want ErrSessionHasNoCategory", err)
	}
}

// A source file removed between WALK and the manual override means a
// re-walk no longer matches the file count WALK originally recorded -- the
// override must refuse rather than place a different set of files than what
// IDENTIFY actually ranked.
func TestManualCommitter_CommitHeldRefusesWhenTheUnitChangedSinceInspection(t *testing.T) {
	fx := newFixture(t, "Artist/Album/01.flac", "Artist/Album/02.flac")
	fx.tool.byPath[filepath.Join(root, "Artist/Album/01.flac")] = mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID}
	fx.tool.byPath[filepath.Join(root, "Artist/Album/02.flac")] = mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID}

	catID := int64(7)
	job := fx.job(t, 0.97)
	if err := fx.run(t, job, importjob.Payload{
		SourcePath: root, Origin: library.ImportOriginManual, CategoryID: &catID,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	sessions := fx.sessions()
	done, _ := sessions.List(t.Context(), library.ImportDone)
	units, _ := sessions.ListUnits(t.Context(), done[0].ID)

	if err := fx.fsys.Remove(filepath.Join(root, "Artist/Album/02.flac")); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	manual := importjob.NewManualCommitter(fx.fsys, fx.tool, sessions, &fakeCommitter{})
	_, err := manual.CommitHeld(t.Context(), units[0].ID)
	if !errors.Is(err, importjob.ErrUnitChanged) {
		t.Errorf("err = %v, want ErrUnitChanged", err)
	}
}

// Two flat "albums" dropped in the same directory with restarting track
// numbers split into two sibling units sharing one rel_path (the same
// structural-fallback shape TestJob_MapsEachUnitInAClusterToItsOwnDecision
// exercises for the inline WALK-time path) -- found live-verifying todo 4.9
// against a real scene fixture: a re-walk by rel_path alone cannot tell the
// siblings apart, so CommitHeld must refuse rather than risk placing the
// wrong sibling's files under this unit's own identity.
func TestManualCommitter_CommitHeldRefusesAUnitThatSharesItsDirectory(t *testing.T) {
	const otherReleaseGroupMBID = "8c08f153-7820-4e0d-89ba-12c034eb022a"
	fx := newFixture(t)
	write := func(relPath string) {
		writeFile(t, fx.fsys, filepath.Join(root, relPath), 200*1024)
		fx.clock.Advance(time.Minute)
	}
	write("Messy/01 First.flac")
	write("Messy/02 Second.flac")
	write("Messy/01 Third.flac")
	write("Messy/02 Fourth.flac")
	fx.tool.byPath[filepath.Join(root, "Messy/01 First.flac")] =
		mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID}
	fx.tool.byPath[filepath.Join(root, "Messy/01 Third.flac")] =
		mediatool.Tags{"musicbrainz_releasegroupid": otherReleaseGroupMBID}

	catID := int64(7)
	job := fx.job(t, 0.97) // above confidenceReleaseGroup (0.95): both queue
	if err := fx.run(t, job, importjob.Payload{
		SourcePath: root, Origin: library.ImportOriginManual, CategoryID: &catID,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	sessions := fx.sessions()
	done, _ := sessions.List(t.Context(), library.ImportDone)
	units, _ := sessions.ListUnits(t.Context(), done[0].ID)
	if len(units) != 2 || units[0].RelPath != units[1].RelPath {
		t.Fatalf("units = %+v, want 2 sharing one rel_path (test setup)", units)
	}

	manual := importjob.NewManualCommitter(fx.fsys, fx.tool, sessions, &fakeCommitter{})
	_, err := manual.CommitHeld(t.Context(), units[0].ID)
	if !errors.Is(err, importjob.ErrUnitSharesDirectory) {
		t.Errorf("err = %v, want ErrUnitSharesDirectory", err)
	}
}

// A session's own placements were already undone by a revert (ClearImportUnitPlacement
// does not touch decision, only work_id/edition_id) -- CommitHeld must not
// resurrect anything into it.
func TestManualCommitter_CommitHeldRefusesAReveredSession(t *testing.T) {
	fx := newFixture(t, "Artist/Album/01.flac", "Artist/Album/02.flac")
	fx.tool.byPath[filepath.Join(root, "Artist/Album/01.flac")] = mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID}
	fx.tool.byPath[filepath.Join(root, "Artist/Album/02.flac")] = mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID}

	catID := int64(7)
	job := fx.job(t, 0.97)
	if err := fx.run(t, job, importjob.Payload{
		SourcePath: root, Origin: library.ImportOriginManual, CategoryID: &catID,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	sessions := fx.sessions()
	done, _ := sessions.List(t.Context(), library.ImportDone)
	units, _ := sessions.ListUnits(t.Context(), done[0].ID)
	if err := sessions.Finish(t.Context(), done[0].ID, library.ImportReverted, fx.clock.Now().Unix(), ""); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	manual := importjob.NewManualCommitter(fx.fsys, fx.tool, sessions, &fakeCommitter{})
	_, err := manual.CommitHeld(t.Context(), units[0].ID)
	if !errors.Is(err, importjob.ErrSessionReverted) {
		t.Errorf("err = %v, want ErrSessionReverted", err)
	}
}

// A session started with no category (Payload.CategoryID nil, so the job's
// own committer -- nil in every test fixture -- never places anything
// inline) reaches DONE with a COMMIT-decided, unplaced unit. PlaceUnplaced
// is what a later "assign a category" action uses to catch it up.
func TestManualCommitter_PlaceUnplacedPlacesEveryUndecidedCommitUnit(t *testing.T) {
	fx := newFixture(t, "Artist/Album/01.flac", "Artist/Album/02.flac")
	fx.tool.byPath[filepath.Join(root, "Artist/Album/01.flac")] = mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID}
	fx.tool.byPath[filepath.Join(root, "Artist/Album/02.flac")] = mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID}

	job := fx.job(t, 0.90) // clears the threshold: COMMIT
	if err := fx.run(t, job, importjob.Payload{SourcePath: root, Origin: library.ImportOriginManual}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	sessions := fx.sessions()
	done, _ := sessions.List(t.Context(), library.ImportDone)
	units, _ := sessions.ListUnits(t.Context(), done[0].ID)
	if units[0].Decision != library.UnitCommit || units[0].WorkID != nil {
		t.Fatalf("unit = %+v, want COMMIT-decided but unplaced (test setup)", units[0])
	}

	if err := sessions.SetCategory(t.Context(), done[0].ID, 9); err != nil {
		t.Fatalf("SetCategory: %v", err)
	}

	committer := &fakeCommitter{workID: seedWork(t, fx)}
	manual := importjob.NewManualCommitter(fx.fsys, fx.tool, sessions, committer)
	placed, errs := manual.PlaceUnplaced(t.Context(), done[0].ID)
	if len(errs) != 0 {
		t.Fatalf("errs = %v, want none", errs)
	}
	if placed != 1 {
		t.Errorf("placed = %d, want 1", placed)
	}
	if len(committer.calls) != 1 || committer.calls[0].categoryID != 9 {
		t.Errorf("calls = %+v, want one call with categoryID=9", committer.calls)
	}

	updated, err := sessions.GetUnit(t.Context(), units[0].ID)
	if err != nil {
		t.Fatalf("GetUnit: %v", err)
	}
	if updated.WorkID == nil {
		t.Error("WorkID still nil after PlaceUnplaced")
	}
}

func TestManualCommitter_PlaceUnplacedRefusesWithNoCategory(t *testing.T) {
	fx := newFixture(t, "Artist/Album/01.flac", "Artist/Album/02.flac")
	fx.tool.byPath[filepath.Join(root, "Artist/Album/01.flac")] = mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID}
	fx.tool.byPath[filepath.Join(root, "Artist/Album/02.flac")] = mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID}

	job := fx.job(t, 0.90)
	if err := fx.run(t, job, importjob.Payload{SourcePath: root, Origin: library.ImportOriginManual}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	sessions := fx.sessions()
	done, _ := sessions.List(t.Context(), library.ImportDone)

	manual := importjob.NewManualCommitter(fx.fsys, fx.tool, sessions, &fakeCommitter{})
	_, errs := manual.PlaceUnplaced(t.Context(), done[0].ID)
	if len(errs) != 1 || !errors.Is(errs[0], importjob.ErrSessionHasNoCategory) {
		t.Errorf("errs = %v, want one ErrSessionHasNoCategory", errs)
	}
}

// One bad unit (fakeCommitter refuses its own sourceRoot) must not stop the
// rest -- the same tolerance placeCommitted already has inline during WALK.
func TestManualCommitter_PlaceUnplacedIsBestEffortAcrossSeveralUnits(t *testing.T) {
	fx := newFixture(t, "Artist/Album1/01.flac", "Artist/Album2/01.flac")
	fx.tool.byPath[filepath.Join(root, "Artist/Album1/01.flac")] = mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID}
	fx.tool.byPath[filepath.Join(root, "Artist/Album2/01.flac")] = mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID}

	job := fx.job(t, 0.90)
	if err := fx.run(t, job, importjob.Payload{SourcePath: root, Origin: library.ImportOriginManual}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	sessions := fx.sessions()
	done, _ := sessions.List(t.Context(), library.ImportDone)
	units, _ := sessions.ListUnits(t.Context(), done[0].ID)
	if len(units) != 2 {
		t.Fatalf("got %d units, want 2 (test setup)", len(units))
	}
	if err := sessions.SetCategory(t.Context(), done[0].ID, 9); err != nil {
		t.Fatalf("SetCategory: %v", err)
	}

	failRoot := filepath.Join(root, units[0].RelPath)
	committer := &fakeCommitter{workID: seedWork(t, fx), errFor: map[string]error{failRoot: errors.New("no room")}}
	manual := importjob.NewManualCommitter(fx.fsys, fx.tool, sessions, committer)
	placed, errs := manual.PlaceUnplaced(t.Context(), done[0].ID)
	if placed != 1 {
		t.Errorf("placed = %d, want 1 (the other unit)", placed)
	}
	if len(errs) != 1 {
		t.Errorf("errs = %v, want exactly 1", errs)
	}
}
