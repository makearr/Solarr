package apiserver_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/db/settings"
)

type fakeImportSessionLister struct {
	sessions []apiserver.ImportSessionRow
	units    []apiserver.ImportUnitRow
	err      error
	// cancelErr is what CancelImportSession returns; cancelled is the last id it was asked to cancel.
	cancelErr error
	cancelled int64
	// dismissErr is what DismissImportSession returns; dismissed is the last
	// id it was asked to hide.
	dismissErr error
	dismissed  int64
	// revertErr/revertResult are what RevertImportSession returns; reverted/lastRetainHours
	// are the last id and retention it was asked to revert with.
	revertErr       error
	revertResult    apiserver.ImportRevertResult
	reverted        int64
	lastRetainHours int

	lastFilterState      string
	lastIncludeDismissed bool
	lastSessionID        int64
	lastFilterDecision   string
	lastAfterID          int64
	lastLimit            int

	// assignErr/assignResult are what AssignImportCategory returns; assignedSession/
	// assignedCategory are the last arguments it was called with.
	assignErr        error
	assignResult     apiserver.ImportPlacementResult
	assignedSession  int64
	assignedCategory int64

	// commitErr/commitResult are what CommitImportUnit returns; commitSessionID/
	// commitUnitID are the last arguments it was called with.
	commitErr       error
	commitResult    apiserver.ImportUnitRow
	commitSessionID int64
	commitUnitID    int64

	// heldErr/heldResult are what CommitHeldImportUnits returns; heldSessionID/
	// heldUnitIDs are the last arguments it was called with.
	heldErr       error
	heldResult    apiserver.ImportPlacementResult
	heldSessionID int64
	heldUnitIDs   []int64
}

func (f *fakeImportSessionLister) CommitHeldImportUnits(
	_ context.Context, sessionID int64, unitIDs []int64,
) (apiserver.ImportPlacementResult, error) {
	f.heldSessionID, f.heldUnitIDs = sessionID, unitIDs
	return f.heldResult, f.heldErr
}

func (f *fakeImportSessionLister) AssignImportCategory(
	_ context.Context, id, categoryID int64,
) (apiserver.ImportPlacementResult, error) {
	f.assignedSession, f.assignedCategory = id, categoryID
	return f.assignResult, f.assignErr
}

func (f *fakeImportSessionLister) CommitImportUnit(
	_ context.Context, sessionID, unitID int64,
) (apiserver.ImportUnitRow, error) {
	f.commitSessionID, f.commitUnitID = sessionID, unitID
	return f.commitResult, f.commitErr
}

func (f *fakeImportSessionLister) CancelImportSession(_ context.Context, id int64) error {
	f.cancelled = id
	return f.cancelErr
}

func (f *fakeImportSessionLister) DismissImportSession(_ context.Context, id int64) error {
	f.dismissed = id
	return f.dismissErr
}

func (f *fakeImportSessionLister) RevertImportSession(
	_ context.Context, id int64, retainHours int,
) (apiserver.ImportRevertResult, error) {
	f.reverted = id
	f.lastRetainHours = retainHours
	return f.revertResult, f.revertErr
}

func (f *fakeImportSessionLister) ListImportSessions(
	_ context.Context, filterState string, includeDismissed bool,
) ([]apiserver.ImportSessionRow, error) {
	f.lastFilterState, f.lastIncludeDismissed = filterState, includeDismissed
	return f.sessions, f.err
}

func (f *fakeImportSessionLister) GetImportSession(
	_ context.Context, id int64,
) (apiserver.ImportSessionRow, error) {
	for _, s := range f.sessions {
		if s.ID == id {
			return s, nil
		}
	}
	if f.err != nil {
		return apiserver.ImportSessionRow{}, f.err
	}
	return apiserver.ImportSessionRow{}, sql.ErrNoRows
}

func (f *fakeImportSessionLister) ListImportUnits(
	_ context.Context, sessionID int64, filterDecision string, afterID int64, limit int,
) ([]apiserver.ImportUnitRow, error) {
	f.lastSessionID, f.lastFilterDecision = sessionID, filterDecision
	f.lastAfterID, f.lastLimit = afterID, limit
	return f.units, f.err
}

type fakeImportInspector struct {
	err error

	lastSourcePath, lastOrigin string
	lastCategoryID             *int64
	lastPreview                bool
	calls                      int
}

func (f *fakeImportInspector) InspectImport(
	_ context.Context, sourcePath, origin string, categoryID *int64, preview bool,
) error {
	f.calls++
	f.lastSourcePath, f.lastOrigin, f.lastCategoryID, f.lastPreview = sourcePath, origin, categoryID, preview
	return f.err
}

func TestListImportSessions_RequiresAuthentication(t *testing.T) {
	fixture := newWorksFixture(t)
	request := authedGet(t, nil, "/api/v1/import/sessions")
	recorder := fixture.do(request)
	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", recorder.Code)
	}
}

func TestListImportSessions_ReturnsWhatTheListerFound(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	started := time.Unix(1000, 0).UTC()
	fixture.importSessions.sessions = []apiserver.ImportSessionRow{
		{ID: 1, SourcePath: "/downloads/a", Origin: "manual", State: "DONE", Threshold: 0.9, StartedAt: started},
	}

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/import/sessions?state=DONE"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", recorder.Code, recorder.Body)
	}
	if fixture.importSessions.lastFilterState != "DONE" {
		t.Errorf("lastFilterState = %q, want DONE", fixture.importSessions.lastFilterState)
	}
	var got []map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(got) != 1 || got[0]["source_path"] != "/downloads/a" {
		t.Fatalf("body = %s, want the one session", recorder.Body)
	}
}

func TestGetImportSession_AnswersNotFoundForAnUnknownID(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/import/sessions/999"))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (%s)", recorder.Code, recorder.Body)
	}
}

func TestGetImportSession_AnswersBadRequestForANonNumericID(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/import/sessions/not-a-number"))
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", recorder.Code)
	}
}

func TestListImportUnits_AnswersNotFoundWhenTheSessionDoesNotExist(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/import/sessions/999/units"))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", recorder.Code)
	}
}

func TestListImportUnits_PassesThroughPaginationAndDecisionFilter(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	fixture.importSessions.sessions = []apiserver.ImportSessionRow{
		{ID: 1, SourcePath: "/downloads/a", Origin: "manual", State: "DONE", Threshold: 0.9},
	}
	fixture.importSessions.units = []apiserver.ImportUnitRow{
		{
			ID: 5, SessionID: 1, RelPath: "Album", Shape: "SET", FileCount: 2, Bytes: 200,
			Confidence: 0.95, Decision: "COMMIT",
			Evidence: apiserver.ImportEvidence{
				Signals:   []apiserver.ImportEvidenceSignal{{Kind: "embedded_release_id", Detail: "rg-1"}},
				Ambiguous: false,
			},
			Candidates: []apiserver.ImportCandidate{
				{Strategy: "embedded-tag", Provider: "musicbrainz:release-group", ExternalID: "rg-1", Confidence: 0.95},
			},
		},
	}

	recorder := fixture.do(authedGet(t, cookies,
		"/api/v1/import/sessions/1/units?decision=COMMIT&after_id=3&limit=10"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", recorder.Code, recorder.Body)
	}
	if fixture.importSessions.lastSessionID != 1 {
		t.Errorf("lastSessionID = %d, want 1", fixture.importSessions.lastSessionID)
	}
	if fixture.importSessions.lastFilterDecision != "COMMIT" {
		t.Errorf("lastFilterDecision = %q, want COMMIT", fixture.importSessions.lastFilterDecision)
	}
	if fixture.importSessions.lastAfterID != 3 {
		t.Errorf("lastAfterID = %d, want 3", fixture.importSessions.lastAfterID)
	}
	if fixture.importSessions.lastLimit != 10 {
		t.Errorf("lastLimit = %d, want 10", fixture.importSessions.lastLimit)
	}

	var got []map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(got) != 1 || got[0]["rel_path"] != "Album" || got[0]["decision"] != "COMMIT" {
		t.Fatalf("body = %s, want the one unit", recorder.Body)
	}
	evidence, ok := got[0]["evidence"].(map[string]any)
	if !ok {
		t.Fatalf("evidence missing or wrong shape: %s", recorder.Body)
	}
	signals, ok := evidence["signals"].([]any)
	if !ok || len(signals) != 1 {
		t.Fatalf("evidence.signals = %v, want one signal", evidence["signals"])
	}
}

func TestInspectImport_RequiresAdministrator(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/import/inspect",
		`{"source_path":"/downloads/incoming"}`))
	if recorder.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (%s)", recorder.Code, recorder.Body)
	}
	if fixture.importInspector.calls != 0 {
		t.Errorf("the inspector ran despite the caller not being an administrator")
	}
}

func TestInspectImport_RejectsAnEmptySourcePath(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/import/inspect", `{"source_path":""}`))
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (%s)", recorder.Code, recorder.Body)
	}
}

func TestInspectImport_EnqueuesAndDefaultsOriginToManual(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/import/inspect",
		`{"source_path":"/downloads/incoming"}`))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (%s)", recorder.Code, recorder.Body)
	}
	if fixture.importInspector.calls != 1 {
		t.Fatalf("the inspector ran %d times, want 1", fixture.importInspector.calls)
	}
	if fixture.importInspector.lastSourcePath != "/downloads/incoming" {
		t.Errorf("lastSourcePath = %q, want /downloads/incoming", fixture.importInspector.lastSourcePath)
	}
	if fixture.importInspector.lastOrigin != "manual" {
		t.Errorf("lastOrigin = %q, want manual (the default)", fixture.importInspector.lastOrigin)
	}
	if fixture.importInspector.lastCategoryID != nil {
		t.Errorf("lastCategoryID = %v, want nil when omitted", fixture.importInspector.lastCategoryID)
	}
}

func TestCancelImportSession_RequiresAdministrator(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/import/sessions/1/cancel", ""))
	if recorder.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (%s)", recorder.Code, recorder.Body)
	}
	if fixture.importSessions.cancelled != 0 {
		t.Error("cancel ran despite the caller not being an administrator")
	}
}

func TestCancelImportSession_AnswersAcceptedOnSuccess(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/import/sessions/42/cancel", ""))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (%s)", recorder.Code, recorder.Body)
	}
	if fixture.importSessions.cancelled != 42 {
		t.Errorf("cancelled = %d, want 42", fixture.importSessions.cancelled)
	}
}

func TestCancelImportSession_AnswersNotFound(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.importSessions.cancelErr = sql.ErrNoRows
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/import/sessions/99/cancel", ""))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (%s)", recorder.Code, recorder.Body)
	}
}

func TestCancelImportSession_AnswersConflictWhenNotRunning(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.importSessions.cancelErr = apiserver.ErrImportSessionNotRunning
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/import/sessions/7/cancel", ""))
	if recorder.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 (%s)", recorder.Code, recorder.Body)
	}
}

func TestRevertImportSession_RequiresAdministrator(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/import/sessions/1/revert", ""))
	if recorder.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (%s)", recorder.Code, recorder.Body)
	}
	if fixture.importSessions.reverted != 0 {
		t.Error("revert ran despite the caller not being an administrator")
	}
}

func TestRevertImportSession_LeavesRetainToTheLibraryWhenAbsent(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.importSessions.revertResult = apiserver.ImportRevertResult{
		UnitsReverted: 2, WorksDeleted: 1, EditionsDeleted: 1, RecycledPaths: []string{"/q/1", "/q/2"},
	}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/import/sessions/42/revert", ""))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", recorder.Code, recorder.Body)
	}
	if fixture.importSessions.reverted != 42 {
		t.Errorf("reverted = %d, want 42", fixture.importSessions.reverted)
	}
	if fixture.importSessions.lastRetainHours != 0 {
		t.Errorf("lastRetainHours = %d, want 0: the library's recycling bin decides", fixture.importSessions.lastRetainHours)
	}
	var body map[string]any
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	paths, _ := body["recycled_paths"].([]any)
	if body["units_reverted"] != float64(2) || body["works_deleted"] != float64(1) || len(paths) != 2 {
		t.Errorf("response = %+v, want the fake's revertResult echoed back", body)
	}
}

func TestRevertImportSession_HonoursAnExplicitRetain(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/import/sessions/42/revert", `{"recycle_retain_hours":24}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", recorder.Code, recorder.Body)
	}
	if fixture.importSessions.lastRetainHours != 24 {
		t.Errorf("lastRetainHours = %d, want 24", fixture.importSessions.lastRetainHours)
	}
}

func TestRevertImportSession_AnswersNotFound(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.importSessions.revertErr = sql.ErrNoRows
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/import/sessions/99/revert", ""))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (%s)", recorder.Code, recorder.Body)
	}
}

func TestRevertImportSession_AnswersConflictWhenNotRevertible(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.importSessions.revertErr = apiserver.ErrImportSessionNotRevertible
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/import/sessions/7/revert", ""))
	if recorder.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 (%s)", recorder.Code, recorder.Body)
	}
}

func TestAssignImportSessionCategory_RequiresAdministrator(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/import/sessions/1/category", `{"category_id":9}`))
	if recorder.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (%s)", recorder.Code, recorder.Body)
	}
	if fixture.importSessions.assignedSession != 0 {
		t.Error("assign ran despite the caller not being an administrator")
	}
}

func TestAssignImportSessionCategory_RejectsAMissingBody(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/import/sessions/1/category", ""))
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (%s)", recorder.Code, recorder.Body)
	}
}

func TestAssignImportSessionCategory_AnswersTheLister(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.importSessions.assignResult = apiserver.ImportPlacementResult{
		UnitsPlaced: 2, Errors: []string{"unit 5: no room"},
	}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/import/sessions/42/category", `{"category_id":9}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", recorder.Code, recorder.Body)
	}
	if fixture.importSessions.assignedSession != 42 || fixture.importSessions.assignedCategory != 9 {
		t.Errorf("assigned = (%d, %d), want (42, 9)",
			fixture.importSessions.assignedSession, fixture.importSessions.assignedCategory)
	}
	var body map[string]any
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	errs, _ := body["errors"].([]any)
	if body["units_placed"] != float64(2) || len(errs) != 1 {
		t.Errorf("response = %+v, want the fake's assignResult echoed back", body)
	}
}

func TestAssignImportSessionCategory_AnswersNotFound(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.importSessions.assignErr = sql.ErrNoRows
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/import/sessions/99/category", `{"category_id":9}`))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (%s)", recorder.Code, recorder.Body)
	}
}

func TestAssignImportSessionCategory_AnswersConflictWhenAlreadySet(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.importSessions.assignErr = apiserver.ErrImportSessionCategoryAlreadySet
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/import/sessions/7/category", `{"category_id":9}`))
	if recorder.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 (%s)", recorder.Code, recorder.Body)
	}
}

func TestAssignImportSessionCategory_AnswersConflictWhenReverted(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.importSessions.assignErr = apiserver.ErrImportSessionReverted
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/import/sessions/7/category", `{"category_id":9}`))
	if recorder.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 (%s)", recorder.Code, recorder.Body)
	}
}

func TestCommitImportUnit_RequiresAdministrator(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/import/sessions/1/units/5/commit", ""))
	if recorder.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (%s)", recorder.Code, recorder.Body)
	}
	if fixture.importSessions.commitUnitID != 0 {
		t.Error("commit ran despite the caller not being an administrator")
	}
}

func TestCommitImportUnit_AnswersTheLister(t *testing.T) {
	fixture := newWorksFixture(t)
	workID := int64(3)
	fixture.importSessions.commitResult = apiserver.ImportUnitRow{
		ID: 5, SessionID: 42, RelPath: "Artist/Album", Decision: "COMMIT", WorkID: &workID,
	}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/import/sessions/42/units/5/commit", ""))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", recorder.Code, recorder.Body)
	}
	if fixture.importSessions.commitSessionID != 42 || fixture.importSessions.commitUnitID != 5 {
		t.Errorf("commit args = (%d, %d), want (42, 5)",
			fixture.importSessions.commitSessionID, fixture.importSessions.commitUnitID)
	}
	var body map[string]any
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["decision"] != "COMMIT" || body["work_id"] != float64(3) {
		t.Errorf("response = %+v, want the fake's commitResult echoed back", body)
	}
}

func TestCommitImportUnit_AnswersNotFound(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.importSessions.commitErr = sql.ErrNoRows
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/import/sessions/1/units/999/commit", ""))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (%s)", recorder.Code, recorder.Body)
	}
}

func TestCommitImportUnit_AnswersConflictVariants(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"not queued", apiserver.ErrImportUnitNotQueued},
		{"no candidate", apiserver.ErrImportUnitHasNoCandidate},
		{"no category", apiserver.ErrImportSessionHasNoCategory},
		{"reverted", apiserver.ErrImportSessionReverted},
		{"changed", apiserver.ErrImportUnitChanged},
		{"no placement", apiserver.ErrNoPlacement},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newWorksFixture(t)
			fixture.importSessions.commitErr = tc.err
			fixture.createUser(t, "admin", settings.RoleAdmin)
			cookies := fixture.signIn(t, "admin")

			recorder := fixture.do(authedPost(t, cookies, "/api/v1/import/sessions/1/units/5/commit", ""))
			if recorder.Code != http.StatusConflict {
				t.Errorf("status = %d, want 409 (%s)", recorder.Code, recorder.Body)
			}
		})
	}
}

func TestInspectImport_PassesPreviewThrough(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       bool
	}{
		{"absent", `{"source_path":"/downloads/incoming"}`, false},
		{"false", `{"source_path":"/downloads/incoming","preview":false}`, false},
		{"true", `{"source_path":"/downloads/incoming","preview":true}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newWorksFixture(t)
			fixture.createUser(t, "admin", settings.RoleAdmin)
			cookies := fixture.signIn(t, "admin")

			recorder := fixture.do(authedPost(t, cookies, "/api/v1/import/inspect", tc.body))
			if recorder.Code != http.StatusAccepted {
				t.Fatalf("status = %d, want 202 (%s)", recorder.Code, recorder.Body)
			}
			if fixture.importInspector.lastPreview != tc.want {
				t.Errorf("preview = %v, want %v", fixture.importInspector.lastPreview, tc.want)
			}
		})
	}
}

func TestCommitHeldImportUnits_RequiresAdministrator(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "viewer", settings.RoleReadonly)
	cookies := fixture.signIn(t, "viewer")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/import/sessions/7/commit", `{}`))
	if recorder.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (%s)", recorder.Code, recorder.Body)
	}
	if fixture.importSessions.heldSessionID != 0 {
		t.Errorf("the commit ran despite the caller not being an administrator")
	}
}

func TestCommitHeldImportUnits_CommitsEverythingWhenNoSubsetIsNamed(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	fixture.importSessions.heldResult = apiserver.ImportPlacementResult{UnitsPlaced: 3}

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/import/sessions/7/commit", `{}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", recorder.Code, recorder.Body)
	}
	if fixture.importSessions.heldSessionID != 7 {
		t.Errorf("session id = %d, want 7", fixture.importSessions.heldSessionID)
	}
	if fixture.importSessions.heldUnitIDs != nil {
		t.Errorf("unit ids = %v, want nil (every held unit)", fixture.importSessions.heldUnitIDs)
	}

	var body gen.ImportCommitHeldResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := gen.ImportCommitHeldResult{UnitsCommitted: 3, Errors: []string{}}
	if !reflect.DeepEqual(body, want) {
		t.Errorf("body = %+v, want %+v", body, want)
	}
}

func TestCommitHeldImportUnits_PassesTheNamedSubsetThrough(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/import/sessions/7/commit",
		`{"unit_ids":[11,13]}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", recorder.Code, recorder.Body)
	}
	if !reflect.DeepEqual(fixture.importSessions.heldUnitIDs, []int64{11, 13}) {
		t.Errorf("unit ids = %v, want [11 13]", fixture.importSessions.heldUnitIDs)
	}
}

func TestCommitHeldImportUnits_RefusesASessionWithNoCategory(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	fixture.importSessions.heldErr = apiserver.ErrImportSessionHasNoCategory

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/import/sessions/7/commit", `{}`))
	if recorder.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 (%s)", recorder.Code, recorder.Body)
	}
}

func TestCommitHeldImportUnits_AnswersNotFoundForAnUnknownSession(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	fixture.importSessions.heldErr = sql.ErrNoRows

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/import/sessions/7/commit", `{}`))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (%s)", recorder.Code, recorder.Body)
	}
}
