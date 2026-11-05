package apiserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
)

// fakeRequests keeps requests in memory with the real store's state guard.
type fakeRequests struct{ records []apiserver.RequestRecord }

func (f *fakeRequests) File(_ context.Context, request apiserver.NewRequestRecord) (apiserver.RequestRecord, error) {
	record := apiserver.RequestRecord{
		ID: int64(len(f.records) + 1), UserID: request.UserID, WorkID: request.WorkID, CategoryID: request.CategoryID,
		ExternalRef: request.ExternalRef, MediaType: request.MediaType, ScopeJSON: request.ScopeJSON, State: request.State,
		CreatedAt: time.Unix(1000, 0).UTC(),
	}
	f.records = append(f.records, record)
	return record, nil
}

func (f *fakeRequests) Get(_ context.Context, id int64) (apiserver.RequestRecord, error) {
	for _, record := range f.records {
		if record.ID == id {
			return record, nil
		}
	}
	return apiserver.RequestRecord{}, apiserver.ErrRequestNotFound
}

func (f *fakeRequests) List(_ context.Context, userID int64, state string, _, _ int64) ([]apiserver.RequestRecord, error) {
	var found []apiserver.RequestRecord
	for index := len(f.records) - 1; index >= 0; index-- {
		record := f.records[index]
		if (userID == 0 || record.UserID == userID) && (state == "" || record.State == state) {
			found = append(found, record)
		}
	}
	return found, nil
}

func (f *fakeRequests) Fulfil(_ context.Context, id, decidedBy, workID int64) (bool, error) {
	for index := range f.records {
		if f.records[index].ID == id && f.records[index].State == "PENDING" {
			f.records[index].State, f.records[index].DecidedBy, f.records[index].WorkID = "FULFILLED", decidedBy, workID
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeRequests) Decline(_ context.Context, id, decidedBy int64, reason string) (bool, error) {
	for index := range f.records {
		if f.records[index].ID == id && f.records[index].State == "PENDING" {
			f.records[index].State, f.records[index].DecidedBy, f.records[index].DeclineReason = "DECLINED", decidedBy, reason
			return true, nil
		}
	}
	return false, nil
}

func requestState(t *testing.T, body []byte) string {
	t.Helper()
	var got struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	return got.State
}

// Someone with only request adds nothing: the add waits as a request an administrator approves, which then adds as the
// requester; a second decision is refused and a decline leaves the library alone.
func TestAddWork_RequestOnlyUserFilesARequestAnAdministratorDecides(t *testing.T) {
	fixture := newWorksFixture(t)
	category, _ := fixture.createCategoryWithRoot(t, "TV", "/media/tv")
	fixture.createUser(t, "admin", settings.RoleAdmin)
	asker := fixture.createUser(t, "asker", settings.RoleOperator)
	if err := fixture.cache.SetCategoryAccess(t.Context(), asker.ID, category.ID, settings.PermissionRequest); err != nil {
		t.Fatal(err)
	}
	fixture.workAdder.result = apiserver.AddWorkResult{Kind: "series", WorkID: 77}
	askerCookies, adminCookies := fixture.signIn(t, "asker"), fixture.signIn(t, "admin")
	body := fmt.Sprintf(`{"category_id":%d,"kind":"series","provider":"tvdb","external_id":"81189","title":"Some Show"}`, category.ID)

	recorder := fixture.do(authedPost(t, askerCookies, "/api/v1/works", body))
	if recorder.Code != http.StatusOK || len(fixture.workAdder.adds) != 0 || len(fixture.requests.records) != 1 {
		t.Fatalf("request: status %d (%s), %d adds, %d requests; want 200, none added, one filed", recorder.Code, recorder.Body, len(fixture.workAdder.adds), len(fixture.requests.records))
	}
	if recorder = fixture.do(authedPost(t, askerCookies, "/api/v1/requests/1/approve", "")); recorder.Code != http.StatusForbidden {
		t.Errorf("approving as the requester: %d, want 403", recorder.Code)
	}
	recorder = fixture.do(authedPost(t, adminCookies, "/api/v1/requests/1/approve", ""))
	if recorder.Code != http.StatusOK || requestState(t, recorder.Body.Bytes()) != "FULFILLED" {
		t.Fatalf("approve: %d (%s)", recorder.Code, recorder.Body)
	}
	if len(fixture.workAdder.adds) != 1 || fixture.workAdder.adds[0].UserID != asker.ID || fixture.workAdder.adds[0].ExternalID != "81189" {
		t.Errorf("approval added %+v, want one add as the requester", fixture.workAdder.adds)
	}
	if recorder = fixture.do(authedPost(t, adminCookies, "/api/v1/requests/1/decline", `{"reason":"no"}`)); recorder.Code != http.StatusConflict {
		t.Errorf("deciding twice: %d, want 409", recorder.Code)
	}

	fixture.do(authedPost(t, askerCookies, "/api/v1/works", body))
	recorder = fixture.do(authedPost(t, adminCookies, "/api/v1/requests/2/decline", `{"reason":"already have it"}`))
	if recorder.Code != http.StatusOK || requestState(t, recorder.Body.Bytes()) != "DECLINED" || len(fixture.workAdder.adds) != 1 {
		t.Errorf("decline: %d (%s), %d adds; want a declined request and no further add", recorder.Code, recorder.Body, len(fixture.workAdder.adds))
	}
}

// An account with auto-approve adds at once and the add is still recorded; the list shows an account only its own.
func TestAddWork_AutoApproveAddsAtOnceAndListsAreScopedToTheCaller(t *testing.T) {
	fixture := newWorksFixture(t)
	category, _ := fixture.createCategoryWithRoot(t, "TV", "/media/tv")
	fixture.createUser(t, "admin", settings.RoleAdmin)
	trusted := fixture.createUser(t, "trusted", settings.RoleOperator)
	other := fixture.createUser(t, "other", settings.RoleOperator)
	for _, user := range []settings.User{trusted, other} {
		if err := fixture.cache.SetCategoryAccess(t.Context(), user.ID, category.ID, settings.PermissionRequest); err != nil {
			t.Fatal(err)
		}
	}
	limits := trusted.Limits
	limits.AutoApprove = true
	if err := fixture.cache.SetUserLimits(t.Context(), trusted.ID, limits); err != nil {
		t.Fatal(err)
	}
	fixture.workAdder.result = apiserver.AddWorkResult{Kind: "series", WorkID: 5}
	body := fmt.Sprintf(`{"category_id":%d,"provider":"tvdb","external_id":"1"}`, category.ID)

	recorder := fixture.do(authedPost(t, fixture.signIn(t, "trusted"), "/api/v1/works", body))
	if recorder.Code != http.StatusOK || len(fixture.workAdder.adds) != 1 || fixture.workAdder.adds[0].UserID != trusted.ID {
		t.Fatalf("auto-approved add: %d (%s), adds %+v", recorder.Code, recorder.Body, fixture.workAdder.adds)
	}
	fixture.do(authedPost(t, fixture.signIn(t, "other"), "/api/v1/works", body))

	for user, want := range map[string]int{"trusted": 1, "other": 1, "admin": 2} {
		recorder = fixture.do(authedGet(t, fixture.signIn(t, user), "/api/v1/requests"))
		var page struct {
			Items []struct{ Username string } `json:"items"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil || len(page.Items) != want {
			t.Errorf("%s sees %d requests (%v), want %d", user, len(page.Items), err, want)
		}
	}
}
