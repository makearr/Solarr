package importjob_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/importing/importjob"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// unplacedSession runs an import whose one unit is decided COMMIT but placed nowhere, then gives it library 9.
func unplacedSession(t *testing.T, fx *fixture) (sessionID int64, unit library.Unit) {
	t.Helper()
	fx.tool.byPath[filepath.Join(root, "Artist/Album/01.flac")] = mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID}
	if err := fx.run(t, fx.job(t, 0.90), importjob.Payload{SourcePath: root, Origin: library.ImportOriginManual}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	sessions := fx.sessions()
	done, _ := sessions.List(t.Context(), library.ImportDone, false)
	units, _ := sessions.ListUnits(t.Context(), done[0].ID)
	if len(units) != 1 || units[0].Decision != library.UnitCommit || units[0].WorkID != nil {
		t.Fatalf("units = %+v, want one COMMIT-decided but unplaced (test setup)", units)
	}
	if err := sessions.SetCategory(t.Context(), done[0].ID, 9); err != nil {
		t.Fatalf("SetCategory: %v", err)
	}
	return done[0].ID, units[0]
}

// A unit its library could not take waits through a run that still finds no room, and is placed by the next.
func TestRetryJob_PlacesWhatALibraryCouldNotTakeOnceItCan(t *testing.T) {
	fx := newFixture(t, "Artist/Album/01.flac")
	_, unit := unplacedSession(t, fx)
	sessions := fx.sessions()

	waiting := library.UnitDecision{UnitID: unit.ID, Decision: library.UnitCommit, Confidence: unit.Confidence,
		CandidatesJSON: unit.CandidatesJSON, Reason: music.ErrNoPlacement.Error()}
	if err := sessions.SetUnitDecisions(t.Context(), unit.SessionID, []library.UnitDecision{waiting}); err != nil {
		t.Fatal(err)
	}
	committer := &fakeCommitter{workID: seedWork(t, fx), errFor: map[string]error{unit.RelPath: music.ErrNoPlacement}}
	retry := importjob.NewRetryJob(sessions, manualCommitter(fx, committer), nil)
	if err := retry.Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if still, _ := sessions.GetUnit(t.Context(), unit.ID); still.Decision != library.UnitCommit || still.WorkID != nil {
		t.Fatalf("unit = %+v, want still waiting while no root can take it", still)
	}

	delete(committer.errFor, unit.RelPath)
	if err := retry.Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	placed, _ := sessions.GetUnit(t.Context(), unit.ID)
	if placed.Decision != library.UnitCommit || placed.WorkID == nil || *placed.WorkID != committer.workID || placed.Reason != "" {
		t.Errorf("unit = %+v, want placed into work %d, no longer saying why it waited", placed, committer.workID)
	}
	if err := retry.Run(t.Context(), jobs.Job{}); err != nil || len(committer.calls) != 2 {
		t.Errorf("a third run: %v, %d commits; want nothing left to retry", err, len(committer.calls))
	}
}

// A unit no try can place any more is closed, so its download fails instead of waiting for ever.
func TestPlaceUnplaced_ClosesWhatCanNeverBePlaced(t *testing.T) {
	for name, tc := range map[string]struct {
		commitErr    error
		removeFile   bool
		wantDecision string
	}{
		"nothing new any more": {commitErr: fmt.Errorf("tv: %w", batch.ErrNothingToPlace), wantDecision: library.UnitSkip},
		"its files are gone":   {removeFile: true, wantDecision: library.UnitFailed},
		"still no room":        {commitErr: music.ErrNoPlacement, wantDecision: library.UnitCommit},
	} {
		t.Run(name, func(t *testing.T) {
			fx := newFixture(t, "Artist/Album/01.flac")
			sessionID, unit := unplacedSession(t, fx)
			committer := &fakeCommitter{workID: seedWork(t, fx), errFor: map[string]error{}}
			if tc.commitErr != nil {
				committer.errFor[unit.RelPath] = tc.commitErr
			}
			if tc.removeFile {
				if err := fx.fsys.Remove(filepath.Join(root, "Artist/Album/01.flac")); err != nil {
					t.Fatal(err)
				}
			}
			if placed, errs := manualCommitter(fx, committer).PlaceUnplaced(t.Context(), sessionID); placed != 0 || len(errs) != 1 {
				t.Fatalf("placed %d, errs %v; want nothing placed and one error", placed, errs)
			}
			got, _ := fx.sessions().GetUnit(t.Context(), unit.ID)
			if got.Decision != tc.wantDecision || got.WorkID != nil {
				t.Errorf("unit = %s %q, want %s and unplaced", got.Decision, got.Reason, tc.wantDecision)
			}
		})
	}
}

// heldRecorder is a type's importer that places every held unit into work, remembering what it was asked.
type heldRecorder struct {
	work     int64
	requests []batch.HeldRequest
}

func (h *heldRecorder) ImportBatch(context.Context, batch.Request) (batch.Result, error) {
	return batch.Result{}, errors.New("not used")
}

func (h *heldRecorder) CommitHeld(_ context.Context, request batch.HeldRequest) (library.Placement, error) {
	h.requests = append(h.requests, request)
	return library.Placement{WorkID: h.work}, nil
}

// A TV or movie unit waiting for room is retried as the work it resolved to, not as a guess to look up again.
func TestPlaceUnplaced_RetriesAWaitingUnitAsTheWorkItResolvedTo(t *testing.T) {
	fx := newFixture(t, "Artist/Album/01.flac")
	sessionID, unit := unplacedSession(t, fx)
	work := seedWork(t, fx)
	waiting := batch.WaitForRoot(library.UnitDecision{UnitID: unit.ID}, work, errors.New("no room"))
	if err := fx.sessions().SetUnitDecisions(t.Context(), sessionID, []library.UnitDecision{waiting}); err != nil {
		t.Fatal(err)
	}
	recorder := &heldRecorder{work: work}
	manual := importjob.NewManualCommitter(fx.fsys, fx.sessions(), library.NewPacks(fx.store), func(int64) batch.Importer { return recorder })
	if placed, errs := manual.PlaceUnplaced(t.Context(), sessionID); placed != 1 || len(errs) != 0 {
		t.Fatalf("placed %d, errs %v; want the unit placed", placed, errs)
	}
	if len(recorder.requests) != 1 || recorder.requests[0].WorkID != work || recorder.requests[0].As != nil {
		t.Errorf("requests = %+v, want one for work %d with nothing to look up", recorder.requests, work)
	}
}

// A music unit its library could not take waits for the retry; any other placing failure is the unit's own.
func TestJob_TellsAUnitNoRootCouldTakeFromOneThatFailed(t *testing.T) {
	for name, tc := range map[string]struct {
		commitErr    error
		wantDecision string
	}{
		"no root can take it": {commitErr: fmt.Errorf("%w: every root is full", music.ErrNoPlacement), wantDecision: library.UnitCommit},
		"it failed to place":  {commitErr: errors.New("copy: input/output error"), wantDecision: library.UnitFailed},
	} {
		t.Run(name, func(t *testing.T) {
			fx := newFixture(t, "Artist/Album/01.flac")
			fx.tool.byPath[filepath.Join(root, "Artist/Album/01.flac")] = mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID}
			committer := &fakeCommitter{errFor: map[string]error{"Artist/Album": tc.commitErr}}
			job := importjob.NewJob(importjob.Config{
				FS: fx.fsys, Sessions: fx.sessions(), Policy: fixedPolicy(0.90), Importers: musicImporters(fx.tool, committer, nil),
				Unassigned: "music", Packs: library.NewPacks(fx.store), Clock: fx.clock, Logger: slog.New(slog.DiscardHandler),
			})
			categoryID := int64(9)
			if err := fx.run(t, job, importjob.Payload{SourcePath: root, Origin: library.ImportOriginManual, CategoryID: &categoryID}); err != nil {
				t.Fatalf("Run: %v", err)
			}
			done, _ := fx.sessions().List(t.Context(), library.ImportDone, false)
			units, _ := fx.sessions().ListUnits(t.Context(), done[0].ID)
			if len(committer.calls) != 1 || len(units) != 1 || units[0].Decision != tc.wantDecision || units[0].WorkID != nil || units[0].Reason != tc.commitErr.Error() {
				t.Errorf("calls %d, units %+v; want one unplaced %s saying %q", len(committer.calls), units, tc.wantDecision, tc.commitErr)
			}
		})
	}
}
