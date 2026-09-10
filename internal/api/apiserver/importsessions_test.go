package apiserver_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/makearr/MediaNexus/internal/api/apiserver"
	"github.com/makearr/MediaNexus/internal/db/settings"
)

type fakeImportSessionLister struct {
	sessions []apiserver.ImportSessionRow
	units    []apiserver.ImportUnitRow
	err      error
	// cancelErr is what CancelImportSession returns; cancelled is the last id it was asked to cancel.
	cancelErr error
	cancelled int64
	// revertErr/revertResult are what RevertImportSession returns; reverted/lastRetainHours
	// are the last id and retention it was asked to revert with.
	revertErr       error
	revertResult    apiserver.ImportRevertResult
	reverted        int64
	lastRetainHours int

	lastFilterState    string
	lastSessionID      int64
	lastFilterDecision string
	lastAfterID        int64
	lastLimit          int
}

func (f *fakeImportSessionLister) CancelImportSession(_ context.Context, id int64) error {
	f.cancelled = id
	return f.cancelErr
}

func (f *fakeImportSessionLister) RevertImportSession(
	_ context.Context, id int64, retainHours int,
) (apiserver.ImportRevertResult, error) {
	f.reverted = id
	f.lastRetainHours = retainHours
	return f.revertResult, f.revertErr
}

func (f *fakeImportSessionLister) ListImportSessions(
	_ context.Context, filterState string,
) ([]apiserver.ImportSessionRow, error) {
	f.lastFilterState = filterState
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
	calls                      int
}

func (f *fakeImportInspector) InspectImport(_ context.Context, sourcePath, origin string, categoryID *int64) error {
	f.calls++
	f.lastSourcePath, f.lastOrigin, f.lastCategoryID = sourcePath, origin, categoryID
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

func TestRevertImportSession_DefaultsRetainToOneWeek(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.importSessions.revertResult = apiserver.ImportRevertResult{
		UnitsReverted: 2, WorksDeleted: 1, EditionsDeleted: 1, QuarantinedPaths: []string{"/q/1", "/q/2"},
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
	if fixture.importSessions.lastRetainHours != 168 {
		t.Errorf("lastRetainHours = %d, want 168 (the default, no body given)", fixture.importSessions.lastRetainHours)
	}
	var body map[string]any
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	paths, _ := body["quarantined_paths"].([]any)
	if body["units_reverted"] != float64(2) || body["works_deleted"] != float64(1) || len(paths) != 2 {
		t.Errorf("response = %+v, want the fake's revertResult echoed back", body)
	}
}

func TestRevertImportSession_HonoursAnExplicitRetain(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/import/sessions/42/revert", `{"quarantine_retain_hours":24}`))
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
