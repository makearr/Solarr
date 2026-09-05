package apiserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/makearr/MediaNexus/internal/apiserver"
	"github.com/makearr/MediaNexus/internal/settings"
)

// fakeBlockedContentLister records the paging args it was called with and
// answers canned rows, so a test can assert query params were parsed and
// passed through without a real library.db.
type fakeBlockedContentLister struct {
	rows          []apiserver.BlockedContentRow
	lastAfterID   int64
	lastLimit     int
	removeErr     error
	lastRemovedID int64
	removeCalls   int
}

func (f *fakeBlockedContentLister) ListBlockedContent(
	_ context.Context, afterID int64, limit int,
) ([]apiserver.BlockedContentRow, error) {
	f.lastAfterID, f.lastLimit = afterID, limit
	return f.rows, nil
}

func (f *fakeBlockedContentLister) RemoveBlockedContent(_ context.Context, id int64) error {
	f.removeCalls++
	f.lastRemovedID = id
	return f.removeErr
}

func TestListBlockedContent_ReturnsCannedRows(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.blockedContent.rows = []apiserver.BlockedContentRow{
		{ID: 1, OSHash: "abc123", SizeBytes: 5000, Reason: "FAKE", Title: "Fake Album",
			Source: "user", CreatedAt: time.Unix(100, 0), HitCount: 3},
	}
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/blocked"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}

	var body []struct {
		ID       int64  `json:"id"`
		Oshash   string `json:"oshash"`
		Title    string `json:"title"`
		HitCount int64  `json:"hit_count"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 1 || body[0].Oshash != "abc123" || body[0].Title != "Fake Album" || body[0].HitCount != 3 {
		t.Errorf("body = %+v, want the one canned row", body)
	}
}

func TestListBlockedContent_PassesThroughAfterIDAndLimit(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/blocked?after_id=42&limit=10"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}
	if fixture.blockedContent.lastAfterID != 42 || fixture.blockedContent.lastLimit != 10 {
		t.Errorf("afterID/limit = %d/%d, want 42/10",
			fixture.blockedContent.lastAfterID, fixture.blockedContent.lastLimit)
	}
}

func TestListBlockedContent_RequiresAuthentication(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)

	recorder := fixture.do(authedGet(t, nil, "/api/v1/blocked"))
	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for no credential", recorder.Code)
	}
}

func TestRemoveBlockedContent_CallsThroughAndAnswers204(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedDelete(t, cookies, "/api/v1/blocked/7"))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if fixture.blockedContent.removeCalls != 1 || fixture.blockedContent.lastRemovedID != 7 {
		t.Errorf("removeCalls=%d lastRemovedID=%d, want 1/7",
			fixture.blockedContent.removeCalls, fixture.blockedContent.lastRemovedID)
	}
}

func TestRemoveBlockedContent_RequiresAdministrator(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedDelete(t, cookies, "/api/v1/blocked/"+strconv.FormatInt(7, 10)))
	if recorder.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for a non-administrator", recorder.Code)
	}
	if fixture.blockedContent.removeCalls != 0 {
		t.Error("RemoveBlockedContent was called despite the caller not being an administrator")
	}
}
