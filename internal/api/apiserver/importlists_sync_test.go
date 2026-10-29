package apiserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/config"
)

// fakeImportListSync previews a list by remembering its stored type and settings, as the real sync does.
type fakeImportListSync struct {
	cache      *settings.Cache
	previewed  map[int64]string
	previewErr error
	checkErr   error
	ran        []int64
	forgotten  []int64
}

func previewKey(list settings.ImportList) string { return list.Type + "|" + list.Options }

func (f *fakeImportListSync) Types(int64) (apiserver.ImportListTypes, error) {
	return apiserver.ImportListTypes{
		Types: []apiserver.ImportListTypeRow{{Type: "anilist_user", Label: "AniList user list", Fields: []apiserver.ImportListFieldRow{
			{Key: "username", Label: "AniList user name", Kind: "text", Required: true, Choices: []apiserver.MediaChoice{}},
		}}},
		MonitorOptions: []apiserver.MediaChoice{{Key: "future", Label: "Future episodes"}},
	}, nil
}

func (f *fakeImportListSync) Check(int64, settings.ImportListRequest) error { return f.checkErr }

func (f *fakeImportListSync) Status(_ context.Context, list settings.ImportList) (apiserver.ImportListStatus, error) {
	status := apiserver.ImportListStatus{Previewed: f.previewed[list.ID] == previewKey(list)}
	if list.ID == 99 {
		status.LastRunAt, status.LastRunAdded, status.LastRunFailure = time.Unix(1_800_000_000, 0), 3, "needs_key"
	}
	return status, nil
}

func (f *fakeImportListSync) Preview(ctx context.Context, listID int64) ([]apiserver.ImportListPreviewRow, error) {
	if f.previewErr != nil {
		return nil, f.previewErr
	}
	list, err := f.cache.ImportList(ctx, listID)
	if err != nil {
		return nil, err
	}
	f.previewed[listID] = previewKey(list)
	return []apiserver.ImportListPreviewRow{{Title: "Example Show", Provider: "anilist", ExternalID: "21", Verdict: "add",
		MatchProvider: "tvdb", MatchExternalID: "501", Year: 2026}}, nil
}

func (f *fakeImportListSync) Run(_ context.Context, listID int64) error {
	f.ran = append(f.ran, listID)
	return nil
}

func (f *fakeImportListSync) Forget(_ context.Context, listID int64) error {
	f.forgotten = append(f.forgotten, listID)
	return nil
}

type importListFixture struct {
	worksFixture
	cookies  []*http.Cookie
	category settings.Category
}

func newImportListFixture(t *testing.T) importListFixture {
	t.Helper()
	fixture := newWorksFixture(t)
	category, err := fixture.cache.CreateCategory(t.Context(), "tv", "Anime", 100)
	if err != nil {
		t.Fatal(err)
	}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	return importListFixture{worksFixture: fixture, cookies: fixture.signIn(t, "admin"), category: category}
}

func (f importListFixture) create(t *testing.T) settings.ImportList {
	t.Helper()
	created, err := f.cache.CreateImportList(t.Context(), f.category.ID, settings.ImportListRequest{
		Name: "Mine", Type: "anilist_user", Options: `{"username":"someone"}`, CadenceHours: 24, Monitor: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return created
}

func (f importListFixture) patch(t *testing.T, id int64, body string) *httptest.ResponseRecorder {
	t.Helper()
	return f.do(authedPatch(t, f.cookies, "/api/v1/import-lists/"+strconv.FormatInt(id, 10), body))
}

func (f importListFixture) post(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	return f.do(authedPost(t, f.cookies, path, ``))
}

const enableMine = `{"name":"Mine","type":"anilist_user","options":{"username":"someone"},"cadence_hours":24,"enabled":true}`

func TestANewListStartsDisabled(t *testing.T) {
	f := newImportListFixture(t)
	recorder := f.do(authedPost(t, f.cookies, fmt.Sprintf("/api/v1/libraries/%d/import-lists", f.category.ID), enableMine))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("enabled on create: status = %d, body = %s", recorder.Code, recorder.Body)
	}
	recorder = f.do(authedPost(t, f.cookies, fmt.Sprintf("/api/v1/libraries/%d/import-lists", f.category.ID),
		`{"name":"Mine","type":"anilist_user","options":{"username":"someone","statuses":" "},"cadence_hours":24,"monitor_option":"future"}`))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var created map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"enabled": false, "previewed": false, "options": map[string]any{"username": "someone"}, "monitor_option": "future"}
	for key, value := range want {
		if !reflect.DeepEqual(created[key], value) {
			t.Errorf("%s = %v, want %v", key, created[key], value)
		}
	}
}

func TestEnablingNeedsAPreviewOfTheSettingsItIsSavedWith(t *testing.T) {
	f := newImportListFixture(t)
	list := f.create(t)
	if recorder := f.patch(t, list.ID, enableMine); recorder.Code != http.StatusConflict {
		t.Fatalf("before a preview: status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if stored, _ := f.cache.ImportList(t.Context(), list.ID); stored.Enabled {
		t.Fatal("a refused enable was stored")
	}
	recorder := f.post(t, "/api/v1/import-lists/"+strconv.FormatInt(list.ID, 10)+"/preview")
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"verdict":"add"`) || !strings.Contains(recorder.Body.String(), `"match_external_id":"501"`) {
		t.Fatalf("preview: status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if recorder := f.patch(t, list.ID, enableMine); recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"previewed":true`) {
		t.Fatalf("after a preview: status = %d, body = %s", recorder.Code, recorder.Body)
	}
	other := `{"name":"Mine","type":"anilist_user","options":{"username":"someone else"},"cadence_hours":24,"enabled":true}`
	if recorder := f.patch(t, list.ID, other); recorder.Code != http.StatusConflict {
		t.Errorf("new settings, still enabled: status = %d, body = %s", recorder.Code, recorder.Body)
	}
	cadence := `{"name":"Mine","type":"anilist_user","options":{"username":"someone"},"cadence_hours":6,"monitor":false,"enabled":true}`
	if recorder := f.patch(t, list.ID, cadence); recorder.Code != http.StatusOK {
		t.Errorf("only the cadence and monitoring changed: status = %d, body = %s", recorder.Code, recorder.Body)
	}
}

func TestAListThatCannotBeReadSaysWhy(t *testing.T) {
	f := newImportListFixture(t)
	list := f.create(t)
	path := "/api/v1/import-lists/" + strconv.FormatInt(list.ID, 10) + "/preview"
	f.importLists.previewErr = apiserver.ImportListReadError{Failure: "needs_key"}
	recorder := f.post(t, path)
	if recorder.Code != http.StatusBadGateway || !strings.Contains(recorder.Body.String(), "TMDB key") {
		t.Errorf("needs key: status = %d, body = %s", recorder.Code, recorder.Body)
	}
	f.importLists.previewErr = fmt.Errorf("wrapped: %w", apiserver.ErrImportListNotSupported)
	if recorder := f.post(t, path); recorder.Code != http.StatusConflict {
		t.Errorf("not supported: status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if recorder := f.post(t, "/api/v1/import-lists/999/preview"); recorder.Code != http.StatusNotFound {
		t.Errorf("missing list: status = %d", recorder.Code)
	}
	if len(f.importLists.previewed) != 0 {
		t.Errorf("a failed preview counted: %v", f.importLists.previewed)
	}
}

func TestRunNowQueuesAnEnabledListOnly(t *testing.T) {
	f := newImportListFixture(t)
	list := f.create(t)
	path := "/api/v1/import-lists/" + strconv.FormatInt(list.ID, 10) + "/run"
	if recorder := f.post(t, path); recorder.Code != http.StatusConflict {
		t.Errorf("disabled: status = %d", recorder.Code)
	}
	f.post(t, "/api/v1/import-lists/"+strconv.FormatInt(list.ID, 10)+"/preview")
	f.patch(t, list.ID, enableMine)
	if recorder := f.post(t, path); recorder.Code != http.StatusAccepted {
		t.Errorf("enabled: status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if !reflect.DeepEqual(f.importLists.ran, []int64{list.ID}) {
		t.Errorf("ran %v", f.importLists.ran)
	}
}

func TestAListTheLibraryCannotTakeIsRefusedWithItsProblems(t *testing.T) {
	f := newImportListFixture(t)
	f.importLists.checkErr = config.Problems{{Path: "type", Value: "tmdb_popular_tv", Expectation: "a list type this library takes"}}
	recorder := f.do(authedPost(t, f.cookies, fmt.Sprintf("/api/v1/libraries/%d/import-lists", f.category.ID),
		`{"name":"Mine","type":"tmdb_popular_tv","cadence_hours":24}`))
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "a list type this library takes") {
		t.Errorf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if lists, _ := f.cache.ImportLists(t.Context(), f.category.ID); len(lists) != 0 {
		t.Errorf("a refused list was stored: %+v", lists)
	}
}

func TestDeletingAListForgetsWhatItDid(t *testing.T) {
	f := newImportListFixture(t)
	list := f.create(t)
	recorder := f.do(authedDelete(t, f.cookies, "/api/v1/import-lists/"+strconv.FormatInt(list.ID, 10)))
	if recorder.Code != http.StatusNoContent || !reflect.DeepEqual(f.importLists.forgotten, []int64{list.ID}) {
		t.Errorf("status = %d, forgotten %v", recorder.Code, f.importLists.forgotten)
	}
}

func TestImportListTypesAreTheLibrarysWithTheirSettings(t *testing.T) {
	f := newImportListFixture(t)
	recorder := f.do(authedGet(t, f.cookies, fmt.Sprintf("/api/v1/libraries/%d/import-list-types", f.category.ID)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	want := `{"monitor_options":[{"key":"future","label":"Future episodes"}],"types":[{"fields":[{"choices":[],"default":"","help":"","key":"username","kind":"text","label":"AniList user name","required":true}],"help":"","label":"AniList user list","type":"anilist_user"}]}`
	if got := strings.TrimSpace(recorder.Body.String()); got != want {
		t.Errorf("body =\n%s\nwant\n%s", got, want)
	}
}
