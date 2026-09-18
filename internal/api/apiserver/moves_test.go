package apiserver_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
)

type fakeDrainStarter struct {
	result apiserver.DrainPlanResult
	err    error

	calls       int
	lastRootID  int64
	lastPreview bool
	stopCalls   int
	lastStopID  int64
	stopErr     error
}

func (f *fakeDrainStarter) StartDrain(_ context.Context, rootFolderID int64, preview bool) (apiserver.DrainPlanResult, error) {
	f.calls++
	f.lastRootID, f.lastPreview = rootFolderID, preview
	return f.result, f.err
}

func (f *fakeDrainStarter) StopDrain(_ context.Context, rootFolderID int64) error {
	f.stopCalls++
	f.lastStopID = rootFolderID
	return f.stopErr
}

type fakeRenameStarter struct {
	result apiserver.DrainPlanResult
	err    error

	calls       int
	lastRootID  int64
	lastPreview bool
}

func (f *fakeRenameStarter) StartRename(_ context.Context, rootFolderID int64, preview bool) (apiserver.DrainPlanResult, error) {
	f.calls++
	f.lastRootID, f.lastPreview = rootFolderID, preview
	return f.result, f.err
}

type fakeMoveJobLister struct {
	rows []apiserver.MoveJobRow
	err  error

	lastFilterReason, lastFilterState string
}

func (f *fakeMoveJobLister) ListMoveJobs(_ context.Context, filterReason, filterState string) ([]apiserver.MoveJobRow, error) {
	f.lastFilterReason, f.lastFilterState = filterReason, filterState
	return f.rows, f.err
}

func TestDrainRootFolder_RequiresAdministrator(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/rootfolders/1/drain", `{}`))
	if recorder.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (%s)", recorder.Code, recorder.Body)
	}
	if fixture.drainStarter.calls != 0 {
		t.Error("drain ran despite the caller not being an administrator")
	}
}

func TestDrainRootFolder_PassesPreviewThrough(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	fixture.drainStarter.result = apiserver.DrainPlanResult{Accepted: false}

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/rootfolders/7/drain", `{"preview":true}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", recorder.Code, recorder.Body)
	}
	if fixture.drainStarter.lastRootID != 7 || !fixture.drainStarter.lastPreview {
		t.Errorf("StartDrain called with (%d, %v), want (7, true)",
			fixture.drainStarter.lastRootID, fixture.drainStarter.lastPreview)
	}
}

func TestDrainRootFolder_DefaultsPreviewToFalse(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/rootfolders/7/drain", `{}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", recorder.Code, recorder.Body)
	}
	if fixture.drainStarter.lastPreview {
		t.Error("preview defaulted to true, want false when omitted")
	}
}

func TestDrainRootFolder_ReturnsTheComputedPlan(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	jobID := int64(42)
	fixture.drainStarter.result = apiserver.DrainPlanResult{
		Accepted: true, MoveJobID: jobID, FilesTotal: 2, BytesTotal: 300,
		Moves: []apiserver.DrainMoveRow{
			{MediaObjectID: 1, DestRootFolderID: 2, DestRelPath: "Artist/01.flac", SizeBytes: 100, AffinityKey: "Artist"},
		},
		RootDeltas: []apiserver.RootDeltaRow{{RootFolderID: 2, BytesIn: 300, ResultingFreeBytes: 700}},
	}

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/rootfolders/1/drain", `{}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", recorder.Code, recorder.Body)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `"accepted":true`) || !strings.Contains(body, `"move_job_id":42`) {
		t.Errorf("body = %s, want accepted:true and move_job_id:42", body)
	}
	if !strings.Contains(body, `"affinity_key":"Artist"`) {
		t.Errorf("body = %s, want the move's affinity_key", body)
	}
}

func TestDrainRootFolder_MapsNothingToDrainTo409(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	fixture.drainStarter.err = apiserver.ErrNothingToDrain

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/rootfolders/1/drain", `{}`))
	if recorder.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 (%s)", recorder.Code, recorder.Body)
	}
}

func TestDrainRootFolder_MapsUnknownRootTo404(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	fixture.drainStarter.err = settings.ErrNotFound

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/rootfolders/99/drain", `{}`))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (%s)", recorder.Code, recorder.Body)
	}
}

func TestStopDrainingRootFolder_RequiresAdministrator(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedDelete(t, cookies, "/api/v1/rootfolders/1/drain"))
	if recorder.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (%s)", recorder.Code, recorder.Body)
	}
	if fixture.drainStarter.stopCalls != 0 {
		t.Error("StopDrain ran despite the caller not being an administrator")
	}
}

func TestStopDrainingRootFolder_Stops(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedDelete(t, cookies, "/api/v1/rootfolders/3/drain"))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (%s)", recorder.Code, recorder.Body)
	}
	if fixture.drainStarter.lastStopID != 3 {
		t.Errorf("StopDrain called with %d, want 3", fixture.drainStarter.lastStopID)
	}
}

func TestListMoveJobs_RequiresAuthentication(t *testing.T) {
	fixture := newWorksFixture(t)
	recorder := fixture.do(authedGet(t, nil, "/api/v1/moves"))
	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", recorder.Code)
	}
}

func TestListMoveJobs_PassesFiltersThrough(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")
	fixture.moveJobs.rows = []apiserver.MoveJobRow{
		{ID: 1, Reason: "DRAIN", State: "RUNNING", FilesTotal: 5, FilesDone: 2, BytesTotal: 500, BytesDone: 200},
	}

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/moves?reason=DRAIN&state=RUNNING"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", recorder.Code, recorder.Body)
	}
	if fixture.moveJobs.lastFilterReason != "DRAIN" || fixture.moveJobs.lastFilterState != "RUNNING" {
		t.Errorf("filters = (%q, %q), want (DRAIN, RUNNING)",
			fixture.moveJobs.lastFilterReason, fixture.moveJobs.lastFilterState)
	}
	if !strings.Contains(recorder.Body.String(), `"files_done":2`) {
		t.Errorf("body = %s, want files_done:2", recorder.Body.String())
	}
}

func TestRenameRootFolder_RequiresAdministrator(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/rootfolders/1/rename", `{}`))
	if recorder.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (%s)", recorder.Code, recorder.Body)
	}
	if fixture.renameStarter.calls != 0 {
		t.Error("rename ran despite the caller not being an administrator")
	}
}

func TestRenameRootFolder_PassesPreviewThrough(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/rootfolders/7/rename", `{"preview":true}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", recorder.Code, recorder.Body)
	}
	if fixture.renameStarter.lastRootID != 7 || !fixture.renameStarter.lastPreview {
		t.Errorf("StartRename called with (%d, %v), want (7, true)",
			fixture.renameStarter.lastRootID, fixture.renameStarter.lastPreview)
	}
}

func TestRenameRootFolder_ReturnsTheComputedPlan(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	jobID := int64(9)
	fixture.renameStarter.result = apiserver.DrainPlanResult{
		Accepted: true, MoveJobID: jobID, FilesTotal: 1, BytesTotal: 100,
		Moves: []apiserver.DrainMoveRow{
			{MediaObjectID: 1, DestRootFolderID: 1, DestRelPath: "Artist/Album (2000)/Artist - 01 - Track.flac", SizeBytes: 100, AffinityKey: "work:1"},
		},
	}

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/rootfolders/1/rename", `{}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", recorder.Code, recorder.Body)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `"accepted":true`) || !strings.Contains(body, `"move_job_id":9`) {
		t.Errorf("body = %s, want accepted:true and move_job_id:9", body)
	}
}

// TestRenameRootFolder_CarriesPerFileWarnings is the naming total-path-
// length recommendation's own HTTP-level proof: DrainMoveRow.Warnings
// reaches the JSON response as warnings, the field a rename preview needs
// to actually show the notice (Invariant 73: preview as a diff).
func TestRenameRootFolder_CarriesPerFileWarnings(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	fixture.renameStarter.result = apiserver.DrainPlanResult{
		Accepted: true, FilesTotal: 1, BytesTotal: 100,
		Moves: []apiserver.DrainMoveRow{
			{
				MediaObjectID: 1, DestRootFolderID: 1, DestRelPath: "Artist/Album (2000)/Artist - 01 - Track.flac",
				SizeBytes: 100, AffinityKey: "work:1",
				Warnings: []string{"rendered path is 300 bytes, over the 240-byte recommended limit"},
			},
		},
	}

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/rootfolders/1/rename", `{"preview":true}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", recorder.Code, recorder.Body)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `"warnings":["rendered path is 300 bytes, over the 240-byte recommended limit"]`) {
		t.Errorf("body = %s, want the move's own warnings array", body)
	}
}

// TestRenameRootFolder_OmitsWarningsWhenThereAreNone confirms a move with no
// warnings does not carry a spurious empty warnings array -- the field is
// optional (omitempty), only populated when there is something to say.
func TestRenameRootFolder_OmitsWarningsWhenThereAreNone(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	fixture.renameStarter.result = apiserver.DrainPlanResult{
		Accepted: true, FilesTotal: 1, BytesTotal: 100,
		Moves: []apiserver.DrainMoveRow{
			{MediaObjectID: 1, DestRootFolderID: 1, DestRelPath: "Artist/Album (2000)/Artist - 01 - Track.flac", SizeBytes: 100, AffinityKey: "work:1"},
		},
	}

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/rootfolders/1/rename", `{"preview":true}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", recorder.Code, recorder.Body)
	}
	if strings.Contains(recorder.Body.String(), `"warnings"`) {
		t.Errorf("body = %s, want no warnings key when there is nothing to warn about", recorder.Body)
	}
}
