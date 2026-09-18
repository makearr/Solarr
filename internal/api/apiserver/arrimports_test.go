package apiserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
)

// fakeArrSync records what it was called with and answers canned results,
// the same shape fakeDiscover already uses.
type fakeArrSync struct {
	run    apiserver.ArrImportRun
	detail apiserver.ArrImportDetail
	runs   []apiserver.ArrImportRun
	revert apiserver.ArrImportRevertResult
	err    error

	lastSyncInstanceID int64
	syncCalls          int
	lastListInstanceID int64
	lastGetImportID    int64
	lastRevertImportID int64
	revertCalls        int

	rootFolders               []string
	lastRootFoldersInstanceID int64
}

func (f *fakeArrSync) Sync(_ context.Context, instanceID int64) (apiserver.ArrImportRun, error) {
	f.syncCalls++
	f.lastSyncInstanceID = instanceID
	if f.err != nil {
		return apiserver.ArrImportRun{}, f.err
	}
	return f.run, nil
}

func (f *fakeArrSync) ListImports(_ context.Context, instanceID int64) ([]apiserver.ArrImportRun, error) {
	f.lastListInstanceID = instanceID
	if f.err != nil {
		return nil, f.err
	}
	return f.runs, nil
}

func (f *fakeArrSync) GetImport(_ context.Context, id int64) (apiserver.ArrImportDetail, error) {
	f.lastGetImportID = id
	if f.err != nil {
		return apiserver.ArrImportDetail{}, f.err
	}
	return f.detail, nil
}

func (f *fakeArrSync) Revert(_ context.Context, id int64) (apiserver.ArrImportRevertResult, error) {
	f.revertCalls++
	f.lastRevertImportID = id
	if f.err != nil {
		return apiserver.ArrImportRevertResult{}, f.err
	}
	return f.revert, nil
}

func (f *fakeArrSync) RootFolders(_ context.Context, instanceID int64) ([]string, error) {
	f.lastRootFoldersInstanceID = instanceID
	if f.err != nil {
		return nil, f.err
	}
	return f.rootFolders, nil
}

func TestSyncArrInstance_ReachesTheSyncerAndRendersTheRun(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	fixture.arrSync.run = apiserver.ArrImportRun{
		ID: 5, InstanceID: 3, App: "lidarr", State: "DONE", ArtistsCreated: 2, AlbumsCreated: 7,
	}

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/arr/instances/3/sync", ""))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if fixture.arrSync.lastSyncInstanceID != 3 || fixture.arrSync.syncCalls != 1 {
		t.Errorf("Sync called with instance=%d, %d times, want 3 once",
			fixture.arrSync.lastSyncInstanceID, fixture.arrSync.syncCalls)
	}
	var body struct {
		ArtistsCreated int64 `json:"artists_created"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.ArtistsCreated != 2 {
		t.Errorf("artists_created = %d, want 2", body.ArtistsCreated)
	}
}

func TestSyncArrInstance_RequiresAdministrator(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "viewer", settings.RoleReadonly)
	cookies := fixture.signIn(t, "viewer")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/arr/instances/3/sync", ""))
	if recorder.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for a non-administrator", recorder.Code)
	}
	if fixture.arrSync.syncCalls != 0 {
		t.Error("Sync was called despite the caller not being an administrator")
	}
}

func TestSyncArrInstance_AppNotYetSupportedAnswers409(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	fixture.arrSync.err = apiserver.ErrArrAppNotYetSupported

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/arr/instances/3/sync", ""))
	if recorder.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 for an app with no real sync yet (%s)", recorder.Code, recorder.Body)
	}
}

func TestListArrInstanceRootFolders_ReachesTheReaderAndRendersPaths(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	fixture.arrSync.rootFolders = []string{"/music", "/music2"}

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/arr/instances/3/rootfolders"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if fixture.arrSync.lastRootFoldersInstanceID != 3 {
		t.Errorf("RootFolders called with instance=%d, want 3", fixture.arrSync.lastRootFoldersInstanceID)
	}
	var body []struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 2 || body[0].Path != "/music" || body[1].Path != "/music2" {
		t.Errorf("root folders = %+v, want [/music /music2]", body)
	}
}

func TestListArrInstanceRootFolders_RequiresAdministrator(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "viewer", settings.RoleReadonly)
	cookies := fixture.signIn(t, "viewer")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/arr/instances/3/rootfolders"))
	if recorder.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for a non-administrator", recorder.Code)
	}
}

func TestListArrImports_ReachesTheListerAndRendersRuns(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	fixture.arrSync.runs = []apiserver.ArrImportRun{{ID: 1, InstanceID: 3, App: "lidarr", State: "DONE"}}

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/arr/imports?instance_id=3"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if fixture.arrSync.lastListInstanceID != 3 {
		t.Errorf("ListImports called with instance=%d, want 3", fixture.arrSync.lastListInstanceID)
	}
}

func TestGetArrImport_ReachesTheReaderAndRendersItems(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	personID := int64(42)
	fixture.arrSync.detail = apiserver.ArrImportDetail{
		Run: apiserver.ArrImportRun{ID: 9, InstanceID: 3, App: "lidarr", State: "DONE"},
		Items: []apiserver.ArrImportItem{
			{ID: 1, ImportID: 9, Kind: "artist", SourceID: "1", Title: "Vutu Doci", PersonID: &personID, PersonCreated: true},
		},
	}

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/arr/imports/9"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if fixture.arrSync.lastGetImportID != 9 {
		t.Errorf("GetImport called with id=%d, want 9", fixture.arrSync.lastGetImportID)
	}
	var body struct {
		Items []struct {
			Title string `json:"title"`
		} `json:"items"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].Title != "Vutu Doci" {
		t.Errorf("items = %+v, want the one Vutu Doci item", body.Items)
	}
}

func TestRevertArrImport_ReachesTheReverterAndRendersTheResult(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	fixture.arrSync.revert = apiserver.ArrImportRevertResult{WorksDeleted: 3, PeopleDeleted: 1}

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/arr/imports/9/revert", ""))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if fixture.arrSync.lastRevertImportID != 9 || fixture.arrSync.revertCalls != 1 {
		t.Errorf("Revert called with id=%d, %d times, want 9 once",
			fixture.arrSync.lastRevertImportID, fixture.arrSync.revertCalls)
	}
}

func TestRevertArrImport_NotDoneAnswers409(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	fixture.arrSync.err = apiserver.ErrArrImportNotDone

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/arr/imports/9/revert", ""))
	if recorder.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 for a non-revertible import (%s)", recorder.Code, recorder.Body)
	}
}
