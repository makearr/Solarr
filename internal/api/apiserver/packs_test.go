package apiserver_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
)

// fakePackLister records the paging args it was called with and answers
// canned rows, so a test can assert query params were parsed and passed
// through without a real library.db.
type fakePackLister struct {
	rows        []apiserver.PackRow
	lastAfterID int64
	lastLimit   int

	getRow   apiserver.PackRow
	getItems []apiserver.PackItemRow
	getErr   error
	lastGet  int64
}

func (f *fakePackLister) ListPacks(_ context.Context, afterID int64, limit int) ([]apiserver.PackRow, error) {
	f.lastAfterID, f.lastLimit = afterID, limit
	return f.rows, nil
}

func (f *fakePackLister) GetPack(_ context.Context, id int64) (apiserver.PackRow, []apiserver.PackItemRow, error) {
	f.lastGet = id
	return f.getRow, f.getItems, f.getErr
}

func TestListPacks_ReturnsCannedRows(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.packs.rows = []apiserver.PackRow{
		{ID: 1, Kind: "DISCOGRAPHY", DetectedFrom: "structure_and_name", Confidence: 0.85,
			Title: "XT+QK", MemberCount: 14, ResolvedCount: 3, CreatedAt: time.Unix(100, 0)},
	}
	fixture.createUser(t, "listener", settings.RoleAdmin)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/packs"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}
	var body []struct {
		ID            int64  `json:"id"`
		Title         string `json:"title"`
		MemberCount   int64  `json:"member_count"`
		ResolvedCount int64  `json:"resolved_count"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 1 || body[0].Title != "XT+QK" || body[0].MemberCount != 14 || body[0].ResolvedCount != 3 {
		t.Errorf("body = %+v, want the one canned row", body)
	}
}

func TestListPacks_PassesThroughAfterIDAndLimit(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleAdmin)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/packs?after_id=42&limit=10"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}
	if fixture.packs.lastAfterID != 42 || fixture.packs.lastLimit != 10 {
		t.Errorf("afterID/limit = %d/%d, want 42/10", fixture.packs.lastAfterID, fixture.packs.lastLimit)
	}
}

func TestListPacks_RequiresAuthentication(t *testing.T) {
	fixture := newWorksFixture(t)
	recorder := fixture.do(authedGet(t, nil, "/api/v1/packs"))
	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for no credential", recorder.Code)
	}
}

func TestGetPack_ReturnsThePackAndItsItems(t *testing.T) {
	fixture := newWorksFixture(t)
	workID := int64(7)
	fixture.packs.getRow = apiserver.PackRow{
		ID: 1, Kind: "DISCOGRAPHY", DetectedFrom: "structure_and_name", Confidence: 0.85,
		Title: "XT+QK", MemberCount: 2, ResolvedCount: 1, CreatedAt: time.Unix(100, 0),
	}
	fixture.packs.getItems = []apiserver.PackItemRow{
		{Ord: 0, WorkID: &workID, State: "RESOLVED"},
		{Ord: 1, State: "QUEUED"},
	}
	fixture.createUser(t, "listener", settings.RoleAdmin)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/packs/1"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}
	if fixture.packs.lastGet != 1 {
		t.Errorf("lastGet = %d, want 1", fixture.packs.lastGet)
	}
	var body struct {
		Pack  struct{ Title string } `json:"pack"`
		Items []struct {
			Ord   int64  `json:"ord"`
			State string `json:"state"`
		} `json:"items"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Pack.Title != "XT+QK" || len(body.Items) != 2 || body.Items[0].State != "RESOLVED" {
		t.Errorf("body = %+v, want XT+QK with 2 items", body)
	}
}

func TestGetPack_AnswersNotFoundForAnUnknownID(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.packs.getErr = sql.ErrNoRows
	fixture.createUser(t, "listener", settings.RoleAdmin)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/packs/999"))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (%s)", recorder.Code, recorder.Body)
	}
}

func TestGetPack_AnswersBadRequestForANonNumericID(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/packs/not-a-number"))
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", recorder.Code)
	}
}
