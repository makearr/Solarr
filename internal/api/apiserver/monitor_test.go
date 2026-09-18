package apiserver_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
)

// fakeWorksMonitor records calls and answers a canned result or error,
// without a real library.db.
type fakeWorksMonitor struct {
	wanted apiserver.WantedRow
	err    error

	lastWorkMonitored *bool
	lastPartID        int64
	lastPartMonitored *bool
	setWorkCalls      int
	setPartCalls      int

	lastTrackPartID int64
	lastTrackWanted *bool
	setTrackCalls   int

	bulkCount             int64
	bulkErr               error
	lastBulkRootFolderIDs []int64
	lastBulkCategoryIDs   []int64
	lastBulkIDs           []int64
	lastBulkExcludedIDs   []int64
	lastBulkMonitored     *bool
	lastBulkPreview       *bool
	bulkCalls             int
}

func (f *fakeWorksMonitor) SetWorkMonitored(_ context.Context, _ int64, monitored bool) (apiserver.WantedRow, error) {
	f.setWorkCalls++
	f.lastWorkMonitored = &monitored
	if f.err != nil {
		return apiserver.WantedRow{}, f.err
	}
	return f.wanted, nil
}

func (f *fakeWorksMonitor) SetPartMonitored(_ context.Context, _, partID int64, monitored bool) error {
	f.setPartCalls++
	f.lastPartID = partID
	f.lastPartMonitored = &monitored
	return f.err
}

func (f *fakeWorksMonitor) SetTrackWanted(_ context.Context, _, partID int64, wanted bool) (apiserver.WantedRow, error) {
	f.setTrackCalls++
	f.lastTrackPartID = partID
	f.lastTrackWanted = &wanted
	if f.err != nil {
		return apiserver.WantedRow{}, f.err
	}
	return f.wanted, nil
}

func (f *fakeWorksMonitor) BulkSetMonitored(
	_ context.Context, rootFolderIDs, categoryIDs, ids, excludedIDs []int64, monitored, preview bool,
) (int64, error) {
	f.bulkCalls++
	f.lastBulkRootFolderIDs = rootFolderIDs
	f.lastBulkCategoryIDs = categoryIDs
	f.lastBulkIDs = ids
	f.lastBulkExcludedIDs = excludedIDs
	f.lastBulkMonitored = &monitored
	f.lastBulkPreview = &preview
	if f.bulkErr != nil {
		return 0, f.bulkErr
	}
	return f.bulkCount, nil
}

func visibleWorksFixture(t *testing.T) (worksFixture, []*http.Cookie) {
	t.Helper()
	fixture := newWorksFixture(t)
	category, root := fixture.createCategoryWithRoot(t, "Music", "/media/music")
	user := fixture.createUser(t, "listener", settings.RoleReadonly)
	if err := fixture.cache.SetCategoryAccess(t.Context(), user.ID, category.ID, settings.PermissionRead); err != nil {
		t.Fatalf("SetCategoryAccess: %v", err)
	}
	cookies := fixture.signIn(t, "listener")
	fixture.workDetail.row = apiserver.WorkDetailRow{
		ID: 1, MediaType: "music", Title: "KI Pecezinu", SortTitle: "ki pecezinu",
		RootFolderIDs: []int64{root.ID},
	}
	return fixture, cookies
}

func TestSetWorkMonitored_TogglesAndReturnsTheWantedState(t *testing.T) {
	fixture, cookies := visibleWorksFixture(t)
	required := int64(12)
	fixture.worksMonitor.wanted = apiserver.WantedRow{
		ID: 5, Monitored: true, RequiredCount: &required, PresentCount: 9, State: "PARTIAL",
	}

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/works/1/monitor", `{"monitored":true}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if fixture.worksMonitor.setWorkCalls != 1 || fixture.worksMonitor.lastWorkMonitored == nil ||
		!*fixture.worksMonitor.lastWorkMonitored {
		t.Errorf("want SetWorkMonitored called once with monitored=true, got %+v", fixture.worksMonitor)
	}

	var body struct {
		ID            int64  `json:"id"`
		Monitored     bool   `json:"monitored"`
		RequiredCount *int64 `json:"required_count"`
		PresentCount  int64  `json:"present_count"`
		State         string `json:"state"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.ID != 5 || !body.Monitored || body.PresentCount != 9 || body.State != "PARTIAL" ||
		body.RequiredCount == nil || *body.RequiredCount != 12 {
		t.Errorf("body = %+v, want the fake's canned Wanted round-tripped", body)
	}
}

// Invariant 37, one level down from GetWork: a work under no readable root
// folder must not be mutable just because its id is known.
func TestSetWorkMonitored_UnderARootFolderTheCallerCannotReadAnswers404(t *testing.T) {
	fixture := newWorksFixture(t)
	_, rootHidden := fixture.createCategoryWithRoot(t, "Private Stash", "/media/private")
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")
	fixture.workDetail.row = apiserver.WorkDetailRow{
		ID: 1, MediaType: "music", Title: "Secret Album", SortTitle: "secret album",
		RootFolderIDs: []int64{rootHidden.ID},
	}

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/works/1/monitor", `{"monitored":true}`))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", recorder.Code)
	}
	if fixture.worksMonitor.setWorkCalls != 0 {
		t.Error("SetWorkMonitored must not be reached for a work the caller cannot even read")
	}
}

func TestSetWorkMonitored_MissingWorkAnswers404(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")
	fixture.workDetail.err = sql.ErrNoRows

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/works/999/monitor", `{"monitored":true}`))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", recorder.Code)
	}
}

func TestSetWorkMonitored_MalformedIDAnswers400(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/works/not-a-number/monitor", `{"monitored":true}`))
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", recorder.Code)
	}
}

func TestSetPartMonitored_TogglesAndAnswers204(t *testing.T) {
	fixture, cookies := visibleWorksFixture(t)

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/works/1/parts/20/monitor", `{"monitored":false}`))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if fixture.worksMonitor.setPartCalls != 1 || fixture.worksMonitor.lastPartID != 20 ||
		fixture.worksMonitor.lastPartMonitored == nil || *fixture.worksMonitor.lastPartMonitored {
		t.Errorf("want SetPartMonitored(work=1, part=20, monitored=false), got %+v", fixture.worksMonitor)
	}
}

func TestSetPartMonitored_MissingPartAnswers404(t *testing.T) {
	fixture, cookies := visibleWorksFixture(t)
	fixture.worksMonitor.err = sql.ErrNoRows

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/works/1/parts/999/monitor", `{"monitored":false}`))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", recorder.Code)
	}
}

// SetTrackWanted is distinct from SetPartMonitored: it stands up the track's
// own SINGLE desired_item rather than resizing the album's required_count
// (todo 4.4), so it answers 200 with the Wanted shape, not 204.
func TestSetTrackWanted_TogglesAndReturnsTheTracksOwnWantedState(t *testing.T) {
	fixture, cookies := visibleWorksFixture(t)
	fixture.worksMonitor.wanted = apiserver.WantedRow{
		ID: 7, Monitored: true, PresentCount: 0, State: "MISSING",
	}

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/works/1/parts/20/wanted", `{"wanted":true}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if fixture.worksMonitor.setTrackCalls != 1 || fixture.worksMonitor.lastTrackPartID != 20 ||
		fixture.worksMonitor.lastTrackWanted == nil || !*fixture.worksMonitor.lastTrackWanted {
		t.Errorf("want SetTrackWanted(work=1, part=20, wanted=true), got %+v", fixture.worksMonitor)
	}

	var body struct {
		ID        int64 `json:"id"`
		Monitored bool  `json:"monitored"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.ID != 7 || !body.Monitored {
		t.Errorf("body = %+v, want the fake's canned Wanted round-tripped", body)
	}
}

func TestSetTrackWanted_MissingPartAnswers404(t *testing.T) {
	fixture, cookies := visibleWorksFixture(t)
	fixture.worksMonitor.err = sql.ErrNoRows

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/works/1/parts/999/wanted", `{"wanted":true}`))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", recorder.Code)
	}
}

// Invariant 37, the same guard SetPartMonitored already has: a work under no
// readable root folder must not be mutable just because its id is known.
func TestSetTrackWanted_UnderARootFolderTheCallerCannotReadAnswers404(t *testing.T) {
	fixture := newWorksFixture(t)
	_, rootHidden := fixture.createCategoryWithRoot(t, "Private Stash", "/media/private")
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")
	fixture.workDetail.row = apiserver.WorkDetailRow{
		ID: 1, MediaType: "music", Title: "Secret Album", SortTitle: "secret album",
		RootFolderIDs: []int64{rootHidden.ID},
	}

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/works/1/parts/20/wanted", `{"wanted":true}`))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", recorder.Code)
	}
	if fixture.worksMonitor.setTrackCalls != 0 {
		t.Error("SetTrackWanted must not be reached for a work the caller cannot even read")
	}
}

func TestBulkSetWorkMonitored_ResolvesCategoryAndForwardsTheSelection(t *testing.T) {
	fixture, cookies := visibleWorksFixture(t)
	fixture.worksMonitor.bulkCount = 7

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/works/bulk/monitor",
		`{"category_id":1,"excluded_ids":[3,4],"monitored":true}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}

	if fixture.worksMonitor.bulkCalls != 1 {
		t.Fatalf("bulkCalls = %d, want 1", fixture.worksMonitor.bulkCalls)
	}
	if len(fixture.worksMonitor.lastBulkRootFolderIDs) == 0 {
		t.Error("want the category resolved to at least one root folder id")
	}
	// The other half of ADR-27 visibility must reach the selection too, or a
	// subscription-discovered work (desired_item.category_id, no root folder
	// yet) can never be matched by a bulk action -- private/next.md's
	// confirmed bug.
	if diff := []int64{1}; len(fixture.worksMonitor.lastBulkCategoryIDs) != len(diff) ||
		fixture.worksMonitor.lastBulkCategoryIDs[0] != 1 {
		t.Errorf("categoryIDs = %v, want %v", fixture.worksMonitor.lastBulkCategoryIDs, diff)
	}
	if len(fixture.worksMonitor.lastBulkIDs) != 0 {
		t.Errorf("ids = %v, want none -- this was a query selection", fixture.worksMonitor.lastBulkIDs)
	}
	if diff := []int64{3, 4}; len(fixture.worksMonitor.lastBulkExcludedIDs) != len(diff) {
		t.Errorf("excludedIDs = %v, want %v", fixture.worksMonitor.lastBulkExcludedIDs, diff)
	}
	if fixture.worksMonitor.lastBulkMonitored == nil || !*fixture.worksMonitor.lastBulkMonitored {
		t.Error("want monitored=true forwarded")
	}
	if fixture.worksMonitor.lastBulkPreview == nil || *fixture.worksMonitor.lastBulkPreview {
		t.Error("want preview=false (the default) forwarded")
	}

	var body struct {
		Count int64 `json:"count"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Count != 7 {
		t.Errorf("count = %d, want 7", body.Count)
	}
}

func TestBulkSetWorkMonitored_ExplicitIDsForwardAsAnIDSelection(t *testing.T) {
	fixture, cookies := visibleWorksFixture(t)

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/works/bulk/monitor",
		`{"ids":[1,2,3],"monitored":false,"preview":true}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if diff := []int64{1, 2, 3}; len(fixture.worksMonitor.lastBulkIDs) != len(diff) {
		t.Errorf("ids = %v, want %v", fixture.worksMonitor.lastBulkIDs, diff)
	}
	if fixture.worksMonitor.lastBulkPreview == nil || !*fixture.worksMonitor.lastBulkPreview {
		t.Error("want preview=true forwarded")
	}
}

// A category the caller cannot read must not resolve to any root folder ids
// -- ADR-27, the same authz.ErrNotVisible ListWorks/GetWork already answer
// 404 for.
func TestBulkSetWorkMonitored_UnreadableCategoryAnswers404(t *testing.T) {
	fixture := newWorksFixture(t)
	_, _ = fixture.createCategoryWithRoot(t, "Private Stash", "/media/private")
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/works/bulk/monitor",
		`{"category_id":1,"monitored":true}`))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", recorder.Code)
	}
	if fixture.worksMonitor.bulkCalls != 0 {
		t.Error("BulkSetMonitored must not be reached for a category the caller cannot read")
	}
}
