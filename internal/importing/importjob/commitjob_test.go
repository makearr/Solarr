package importjob_test

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/importing/importjob"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

func TestCommitDefinition_RunsInTheImportPool(t *testing.T) {
	if importjob.CommitDefinition.Pool != jobs.PoolImport || !importjob.CommitDefinition.Mutating {
		t.Errorf("definition = %+v, want a mutating job in the import pool", importjob.CommitDefinition)
	}
	if importjob.CommitActionKey(4) == importjob.CommitActionKey(5) {
		t.Error("two sessions share an action key; one would absorb the other's placement")
	}
}

func commitPayload(t *testing.T, payload importjob.CommitPayload) jobs.Job {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return jobs.Job{Payload: raw}
}

func TestCommitJob_CommitsOnlyTheNamedHeldUnits(t *testing.T) {
	fx := newFixture(t, "Artist/Album1/01.flac", "Artist/Album2/01.flac")
	fx.tool.byPath[filepath.Join(root, "Artist/Album1/01.flac")] = mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID}
	fx.tool.byPath[filepath.Join(root, "Artist/Album2/01.flac")] = mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID}
	payload := importjob.Payload{SourcePath: root, Origin: library.ImportOriginManual, Preview: true}
	if err := fx.run(t, fx.job(t, 0.90), payload); err != nil {
		t.Fatalf("Run: %v", err)
	}
	sessions := fx.sessions()
	done, _ := sessions.List(t.Context(), library.ImportDone, false)
	units, _ := sessions.ListUnits(t.Context(), done[0].ID)
	if err := sessions.SetCategory(t.Context(), done[0].ID, 9); err != nil {
		t.Fatalf("SetCategory: %v", err)
	}

	committer := &fakeCommitter{workID: seedWork(t, fx)}
	handler := importjob.NewCommitJob(sessions, manualCommitter(fx, committer), nil, nil)
	if err := handler.Run(t.Context(), commitPayload(t, importjob.CommitPayload{SessionID: done[0].ID, UnitIDs: []int64{units[1].ID}})); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if left, _ := sessions.GetUnit(t.Context(), units[0].ID); left.WorkID != nil {
		t.Error("the unit that was not named got committed anyway")
	}
	if placed, _ := sessions.GetUnit(t.Context(), units[1].ID); placed.WorkID == nil {
		t.Error("the named unit was not committed")
	}
}

func TestCommitJob_PlacesWhatALibraryWasGiven(t *testing.T) {
	fx := newFixture(t, "Artist/Album/01.flac")
	sessionID, unit := unplacedSession(t, fx)
	committer := &fakeCommitter{workID: seedWork(t, fx)}
	handler := importjob.NewCommitJob(fx.sessions(), manualCommitter(fx, committer), nil, nil)
	if err := handler.Run(t.Context(), commitPayload(t, importjob.CommitPayload{SessionID: sessionID, Unplaced: true})); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if placed, _ := fx.sessions().GetUnit(t.Context(), unit.ID); placed.WorkID == nil {
		t.Error("the decided unit was not placed")
	}
}
