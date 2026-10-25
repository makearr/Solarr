package apiserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
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
	lastSyncOptions    apiserver.ArrSyncOptions
	preview            apiserver.ArrImportPreview
	syncCalls          int
	lastListInstanceID int64
	lastGetImportID    int64
	lastRevertImportID int64
	revertCalls        int

	rootFolders               []string
	lastRootFoldersInstanceID int64
}

func (f *fakeArrSync) Preview(_ context.Context, instanceID int64, options apiserver.ArrSyncOptions) (apiserver.ArrImportPreview, error) {
	f.lastSyncInstanceID = instanceID
	f.lastSyncOptions = options
	return f.preview, f.err
}

func (f *fakeArrSync) Sync(_ context.Context, instanceID int64, options apiserver.ArrSyncOptions) (apiserver.ArrImportRun, error) {
	f.syncCalls++
	f.lastSyncInstanceID = instanceID
	f.lastSyncOptions = options
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

// The import options (2026-09-30): absent is the old behaviour, a body names what to bring in.
func TestSyncArrInstance_ReadsTheImportOptions(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	if recorder := fixture.do(authedPost(t, cookies, "/api/v1/arr/instances/3/sync", "")); recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if !reflect.DeepEqual(fixture.arrSync.lastSyncOptions, apiserver.DefaultArrSyncOptions) {
		t.Errorf("no body: options = %+v, want the defaults", fixture.arrSync.lastSyncOptions)
	}

	body := `{"monitored":false,"unmonitored":true,"albums":"missing","path_rewrites":[{"from":"/media/music","to":"/mnt/media/music"}]}`
	if recorder := fixture.do(authedPost(t, cookies, "/api/v1/arr/instances/3/sync", body)); recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	want := apiserver.ArrSyncOptions{
		Unmonitored: true, Albums: "missing",
		PathRewrites: []apiserver.PathRewrite{{From: "/media/music", To: "/mnt/media/music"}},
	}
	if !reflect.DeepEqual(fixture.arrSync.lastSyncOptions, want) {
		t.Errorf("options = %+v, want %+v", fixture.arrSync.lastSyncOptions, want)
	}

	if recorder := fixture.do(authedPost(t, cookies, "/api/v1/arr/instances/3/sync", `{"monitored":false}`)); recorder.Code != http.StatusBadRequest {
		t.Errorf("neither monitored nor unmonitored: status = %d, want 400", recorder.Code)
	}
}

// ADR-52's preview: the mapping a person chose rides in the options, and the preview answers what each root folder,
// profile and library would get.
func TestPreviewArrImport_ReadsTheMappingAndRendersThePreview(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	library, profile := int64(4), int64(9)
	fixture.arrSync.preview = apiserver.ArrImportPreview{
		ItemKind: "series",
		Roots:    []apiserver.ArrPreviewRoot{{Path: "/tv", LocalPath: "/mnt/tv", LibraryID: &library, Chosen: 3, New: 1, InLibrary: 1, WithoutID: 1}},
		Profiles: []apiserver.ArrPreviewProfile{{ArrProfileID: 6, Name: "Any", Items: 3, ProposedProfileID: &profile}},
		Settings: []apiserver.ArrPreviewSettings{{LibraryID: 4, Changes: []apiserver.ArrSettingChange{{Key: "delete_empty_folders", Label: "Delete empty folders", Current: "Off", Proposed: "On"}}}},
	}

	body := `{"profile_map":[{"arr_profile_id":6,"profile_id":9},{"arr_profile_id":7,"profile_id":null}],"settings_into":[4],"new_folders_library_id":4}`
	recorder := fixture.do(authedPost(t, cookies, "/api/v1/arr/instances/3/preview", body))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	want := apiserver.ArrSyncOptions{
		Monitored: true, Albums: "all", ProfileMap: map[int64]*int64{6: &profile, 7: nil}, SettingsInto: []int64{4}, NewFoldersLibrary: &library,
	}
	if !reflect.DeepEqual(fixture.arrSync.lastSyncOptions, want) {
		t.Errorf("options = %+v, want %+v", fixture.arrSync.lastSyncOptions, want)
	}
	var got map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	wantBody := map[string]any{
		"item_kind": "series",
		"roots": []any{map[string]any{"path": "/tv", "local_path": "/mnt/tv", "root_folder_id": nil, "library_id": float64(4), "chosen": float64(3),
			"new": float64(1), "in_library": float64(1), "without_id": float64(1)}},
		"profiles": []any{map[string]any{"arr_profile_id": float64(6), "name": "Any", "items": float64(3), "proposed_profile_id": float64(9)}},
		"settings": []any{map[string]any{"library_id": float64(4), "changes": []any{map[string]any{
			"key": "delete_empty_folders", "label": "Delete empty folders", "current": "Off", "proposed": "On"}}}},
	}
	if !reflect.DeepEqual(got, wantBody) {
		t.Errorf("body = %v\nwant %v", got, wantBody)
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
