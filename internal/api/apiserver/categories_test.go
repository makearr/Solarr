package apiserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
)

func authedDelete(t *testing.T, cookies []*http.Cookie, target string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(http.MethodDelete, target, nil)
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	return request
}

// fakeLibrarySize answers canned counter values per scope, so a test can assert
// ListCategories reads "works"/"files" from exactly the scope key
// library.CategoryCounters writes to ("category:<id>") without a real library.db.
type fakeLibrarySize struct {
	byScope map[string]map[string]int64
}

func (f fakeLibrarySize) Counters(_ context.Context, scope string) (map[string]int64, error) {
	values, ok := f.byScope[scope]
	if !ok {
		return nil, errors.New("fake: no counters for scope " + scope)
	}
	return values, nil
}

// TestListCategories_ReturnsOnlyGrantedCategories is the core ADR-27
// guarantee: a category the caller was not granted must never appear, and one
// they were must, in the {id, media_type, name} shape (no permission field --
// that belongs to GetMe, not this navigation endpoint).
func TestListCategories_ReturnsOnlyGrantedCategories(t *testing.T) {
	fixture := newWorksFixture(t)
	visible, err := fixture.cache.CreateCategory(t.Context(), "music", "Music", 100)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	hidden, err := fixture.cache.CreateCategory(t.Context(), "tv", "Private Stash", 100)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	user := fixture.createUser(t, "listener", settings.RoleReadonly)
	if err := fixture.cache.SetCategoryAccess(t.Context(), user.ID, visible.ID, settings.PermissionRead); err != nil {
		t.Fatalf("SetCategoryAccess: %v", err)
	}
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/categories"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}

	var body []struct {
		ID        int64  `json:"id"`
		MediaType string `json:"media_type"`
		Name      string `json:"name"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 1 {
		t.Fatalf("categories = %+v, want exactly the one granted category", body)
	}
	if body[0].ID != visible.ID || body[0].MediaType != "music" || body[0].Name != "Music" {
		t.Errorf("category = %+v, want {%d music Music}", body[0], visible.ID)
	}
	for _, category := range body {
		if category.ID == hidden.ID {
			t.Errorf("categories = %+v, must not include the ungranted category %d", body, hidden.ID)
		}
	}
}

// An admin is a system identity (authz.Access.system), which VisibleCategoryIDs
// treats as "every category" -- mirrors works_test.go's equivalent case.
func TestListCategories_AdminSeesEveryCategory(t *testing.T) {
	fixture := newWorksFixture(t)
	first, err := fixture.cache.CreateCategory(t.Context(), "music", "Music", 100)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	second, err := fixture.cache.CreateCategory(t.Context(), "tv", "Television", 100)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/categories"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}

	var body []struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	seen := map[int64]bool{}
	for _, category := range body {
		seen[category.ID] = true
	}
	if !seen[first.ID] || !seen[second.ID] {
		t.Errorf("categories = %+v, want both %d and %d", body, first.ID, second.ID)
	}
}

// A user granted nothing must see an empty array, never null -- a frontend
// that maps over the response should not have to guard against null.
func TestListCategories_NoGrantsReturnsEmptyArray(t *testing.T) {
	fixture := newWorksFixture(t)
	if _, err := fixture.cache.CreateCategory(t.Context(), "music", "Music", 100); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	fixture.createUser(t, "nobody", settings.RoleReadonly)
	cookies := fixture.signIn(t, "nobody")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/categories"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}
	if recorder.Body.String() == "null\n" || recorder.Body.String() == "null" {
		t.Fatalf("body = %q, want an empty array, not null", recorder.Body.String())
	}

	var body []json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 0 {
		t.Errorf("categories = %v, want empty: this user has no category grants", body)
	}
}

// Counts come from the counter table via the scope key
// library.CategoryCounters writes ("category:<id>"), never a live COUNT.
func TestListCategories_IncludesCountsFromTheCounterTable(t *testing.T) {
	librarySize := fakeLibrarySize{byScope: map[string]map[string]int64{}}
	fixture := newWorksFixtureWithLibrarySize(t, librarySize)
	category, err := fixture.cache.CreateCategory(t.Context(), "music", "Music", 100)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	librarySize.byScope["category:"+strconv.FormatInt(category.ID, 10)] = map[string]int64{
		"works": 42, "files": 137,
	}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/categories"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}

	var body []struct {
		ID    int64 `json:"id"`
		Works int   `json:"works"`
		Files int   `json:"files"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 1 || body[0].Works != 42 || body[0].Files != 137 {
		t.Errorf("categories = %+v, want works=42 files=137", body)
	}
}

// A category the counters sweep has never reconciled must answer 0, not fail
// the whole list -- one figure being unavailable should not hide navigation.
func TestListCategories_UnreconciledCategoryAnswersZero(t *testing.T) {
	librarySize := fakeLibrarySize{byScope: map[string]map[string]int64{}}
	fixture := newWorksFixtureWithLibrarySize(t, librarySize)
	if _, err := fixture.cache.CreateCategory(t.Context(), "music", "Music", 100); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/categories"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}

	var body []struct {
		Works int `json:"works"`
		Files int `json:"files"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 1 || body[0].Works != 0 || body[0].Files != 0 {
		t.Errorf("categories = %+v, want works=0 files=0, not an error", body)
	}
}

// Unauthenticated requests answer 401, same as every other route in the
// secured group.
func TestListCategories_RequiresAuthentication(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)

	recorder := fixture.do(authedGet(t, nil, "/api/v1/categories"))
	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for no credential", recorder.Code)
	}
}

// CreateCategory is the manual half of what a hand-written settings/import
// YAML document already did -- an administrator can now stand up a category
// without one.
func TestCreateCategory_AddsACategoryAdministratorsCanSeeImmediately(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/categories",
		`{"media_type":"music","name":"Music"}`))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}

	var created struct {
		ID        int64  `json:"id"`
		MediaType string `json:"media_type"`
		Name      string `json:"name"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if created.MediaType != "music" || created.Name != "Music" || created.ID == 0 {
		t.Errorf("created = %+v, want a real id and the submitted fields", created)
	}

	category, found := fixture.cache.Snapshot().Category(created.ID)
	if !found {
		t.Fatalf("category %d not found in settings.db after creation", created.ID)
	}
	if category.Name != "Music" {
		t.Errorf("stored category = %+v, want Name=Music", category)
	}
}

// An omitted priority_rank must place the new category after every existing
// one, so creating one from the interface never requires understanding
// ADR-36's ordering just to get a sane default.
func TestCreateCategory_OmittedPriorityRankPlacesItLast(t *testing.T) {
	fixture := newWorksFixture(t)
	if _, err := fixture.cache.CreateCategory(t.Context(), "music", "Music", 5); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/categories",
		`{"media_type":"tv","name":"Television"}`))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	category, found := fixture.cache.Snapshot().Category(created.ID)
	if !found || category.PriorityRank <= 5 {
		t.Errorf("priority rank = %+v, want something after the existing category's 5", category)
	}
}

// A non-administrator must not be able to create a category -- the same gate
// settings import/export already enforces.
func TestCreateCategory_RequiresAdministrator(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/categories",
		`{"media_type":"music","name":"Music"}`))
	if recorder.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for a non-administrator", recorder.Code)
	}
}

// Invalid input (an empty name) must answer 400 with the validation problem,
// not a generic 500 -- config.Problems has its own shape writeError does not
// special-case, so the handler must translate it itself (settings.go's
// ImportSettings is the precedent).
func TestCreateCategory_InvalidInputAnswers400(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/categories",
		`{"media_type":"music","name":""}`))
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for an empty name, body = %s", recorder.Code, recorder.Body)
	}
}

// GetCategory answers the richer settings-side detail ListCategories
// deliberately never carries (priority_rank, the two profile ids,
// monitor_discovered).
func TestGetCategory_ReturnsTheFullDetail(t *testing.T) {
	fixture := newWorksFixture(t)
	category, err := fixture.cache.CreateCategory(t.Context(), "music", "Music", 10)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/categories/"+strconv.FormatInt(category.ID, 10)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}

	var body struct {
		Name              string `json:"name"`
		PriorityRank      int64  `json:"priority_rank"`
		MonitorDiscovered string `json:"monitor_discovered"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Name != "Music" || body.PriorityRank != 10 || body.MonitorDiscovered != "monitored" {
		t.Errorf("body = %+v, want Name=Music PriorityRank=10 MonitorDiscovered=monitored", body)
	}
}

func TestGetCategory_RequiresAdministrator(t *testing.T) {
	fixture := newWorksFixture(t)
	category, err := fixture.cache.CreateCategory(t.Context(), "music", "Music", 10)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/categories/"+strconv.FormatInt(category.ID, 10)))
	if recorder.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for a non-administrator", recorder.Code)
	}
}

// UpdateCategory is todo 4.6's own remaining gap: creation and deletion
// existed, editing did not.
func TestUpdateCategory_ChangesNameRankAndProfiles(t *testing.T) {
	fixture := newWorksFixture(t)
	category, err := fixture.cache.CreateCategory(t.Context(), "music", "Music", 10)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	quality, err := fixture.cache.CreateQualityProfile(t.Context(), "Lossless", "{}", "{}")
	if err != nil {
		t.Fatalf("CreateQualityProfile: %v", err)
	}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	body := `{"name":"Tunes","priority_rank":5,"quality_profile_id":` +
		strconv.FormatInt(quality.ID, 10) + `,"monitor_discovered":"none"}`
	recorder := fixture.do(authedPatch(t, cookies, "/api/v1/categories/"+strconv.FormatInt(category.ID, 10), body))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}

	updated, ok := fixture.cache.Snapshot().Category(category.ID)
	if !ok {
		t.Fatalf("category %d not found after update", category.ID)
	}
	if updated.Name != "Tunes" || updated.PriorityRank != 5 || updated.MonitorDiscovered != "none" {
		t.Errorf("updated = %+v, want Name=Tunes PriorityRank=5 MonitorDiscovered=none", updated)
	}
	if updated.QualityProfileID == nil || *updated.QualityProfileID != quality.ID {
		t.Errorf("QualityProfileID = %v, want %d", updated.QualityProfileID, quality.ID)
	}
}

func TestUpdateCategory_InvalidInputAnswers400(t *testing.T) {
	fixture := newWorksFixture(t)
	category, err := fixture.cache.CreateCategory(t.Context(), "music", "Music", 10)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPatch(t, cookies, "/api/v1/categories/"+strconv.FormatInt(category.ID, 10),
		`{"name":"","priority_rank":5}`))
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for an empty name, body = %s", recorder.Code, recorder.Body)
	}
}

func TestUpdateCategory_RequiresAdministrator(t *testing.T) {
	fixture := newWorksFixture(t)
	category, err := fixture.cache.CreateCategory(t.Context(), "music", "Music", 10)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedPatch(t, cookies, "/api/v1/categories/"+strconv.FormatInt(category.ID, 10),
		`{"name":"Tunes","priority_rank":5}`))
	if recorder.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for a non-administrator", recorder.Code)
	}
}

func TestUpdateCategory_MissingCategoryAnswers404(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPatch(t, cookies, "/api/v1/categories/999999",
		`{"name":"Tunes","priority_rank":5}`))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a missing category", recorder.Code)
	}
}

// DeleteCategory removes an unreferenced category. Every administrator holds
// an automatic manage grant on every category (settings.CreateUser/
// CreateCategory keep that in sync both directions), so the admin issuing the
// request has to give up its own grant first -- the same real constraint
// internal/db/settings' own TestDeleteUser_TakesTheirGrantsWithThem works around
// by testing its "successful delete" case with an operator instead. Here the
// request itself must come from an admin (CreateCategory/DeleteCategory are
// admin-only), so the admin's own auto-granted access is revoked first.
func TestDeleteCategory_RemovesAnUnreferencedCategory(t *testing.T) {
	fixture := newWorksFixture(t)
	category, err := fixture.cache.CreateCategory(t.Context(), "music", "Music", 1)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	admin := fixture.createUser(t, "admin", settings.RoleAdmin)
	if err := fixture.cache.RevokeCategoryAccess(t.Context(), admin.ID, category.ID); err != nil {
		t.Fatalf("RevokeCategoryAccess: %v", err)
	}
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedDelete(t, cookies, "/api/v1/categories/"+strconv.FormatInt(category.ID, 10)))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if _, found := fixture.cache.Snapshot().Category(category.ID); found {
		t.Error("category still present in settings.db after delete")
	}
}

// A category with an attached root folder must be refused, naming what still
// uses it (Invariant 41), the same as deleting an in-use quality profile.
func TestDeleteCategory_RefusedWhileARootFolderIsAttached(t *testing.T) {
	fixture := newWorksFixture(t)
	category, _ := fixture.createCategoryWithRoot(t, "Music", "/media/music")
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedDelete(t, cookies, "/api/v1/categories/"+strconv.FormatInt(category.ID, 10)))
	if recorder.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 while a root folder is still attached, body = %s",
			recorder.Code, recorder.Body)
	}
}

// A non-administrator must not be able to delete a category either.
func TestDeleteCategory_RequiresAdministrator(t *testing.T) {
	fixture := newWorksFixture(t)
	category, err := fixture.cache.CreateCategory(t.Context(), "music", "Music", 1)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedDelete(t, cookies, "/api/v1/categories/"+strconv.FormatInt(category.ID, 10)))
	if recorder.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for a non-administrator", recorder.Code)
	}
}

// Deleting a category that does not exist answers 404, not 204 or 500.
func TestDeleteCategory_MissingCategoryAnswers404(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedDelete(t, cookies, "/api/v1/categories/999999"))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a missing category", recorder.Code)
	}
}

// Attach is the only way a fresh install's root folder ever gets into a
// category -- neither screen offered it until this endpoint existed (found
// live: an added root folder never placed anything, since nothing attached
// it to a category).
func TestAttachRootFolderToCategory_PutsTheRootIntoTheCategory(t *testing.T) {
	fixture := newWorksFixture(t)
	category, err := fixture.cache.CreateCategory(t.Context(), "music", "Music", 1)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	root, err := fixture.cache.CreateRootFolder(t.Context(), "/media/music", "")
	if err != nil {
		t.Fatalf("CreateRootFolder: %v", err)
	}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	target := "/api/v1/categories/" + strconv.FormatInt(category.ID, 10) + "/rootfolders"
	body := `{"root_folder_id":` + strconv.FormatInt(root.ID, 10) + `}`
	recorder := fixture.do(authedPost(t, cookies, target, body))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}

	attached := fixture.cache.Snapshot().CategoriesForRootFolder(root.ID)
	if len(attached) != 1 || attached[0] != category.ID {
		t.Errorf("CategoriesForRootFolder = %v, want [%d]", attached, category.ID)
	}
}

// A folder attached to a library is scanned at once, not at the next six-hourly scan: the setup
// wizard's library showed nothing for hours (found 2026-09-28).
func TestAttachRootFolderToCategory_StartsTheLibraryScan(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.scheduler.tasks = []jobs.Task{{Key: "scan", DefaultCadence: "0 */6 * * *"}}
	category, err := fixture.cache.CreateCategory(t.Context(), "music", "Music", 1)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	root, err := fixture.cache.CreateRootFolder(t.Context(), "/media/music", "")
	if err != nil {
		t.Fatalf("CreateRootFolder: %v", err)
	}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	target := "/api/v1/categories/" + strconv.FormatInt(category.ID, 10) + "/rootfolders"
	recorder := fixture.do(authedPost(t, cookies, target, `{"root_folder_id":`+strconv.FormatInt(root.ID, 10)+`}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if fixture.scheduler.lastRunKey != "scan" || fixture.scheduler.lastRunFull {
		t.Errorf("ran %q (full=%v), want an ordinary scan", fixture.scheduler.lastRunKey, fixture.scheduler.lastRunFull)
	}
}

// A non-administrator must not be able to attach a root folder either.
func TestAttachRootFolderToCategory_RequiresAdministrator(t *testing.T) {
	fixture := newWorksFixture(t)
	category, err := fixture.cache.CreateCategory(t.Context(), "music", "Music", 1)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	root, err := fixture.cache.CreateRootFolder(t.Context(), "/media/music", "")
	if err != nil {
		t.Fatalf("CreateRootFolder: %v", err)
	}
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	target := "/api/v1/categories/" + strconv.FormatInt(category.ID, 10) + "/rootfolders"
	body := `{"root_folder_id":` + strconv.FormatInt(root.ID, 10) + `}`
	recorder := fixture.do(authedPost(t, cookies, target, body))
	if recorder.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for a non-administrator", recorder.Code)
	}
}

// Detach is the reverse: DeleteCategory's own 409 refusal (above) stays
// refused until this actually removes the attachment.
func TestDetachRootFolderFromCategory_RemovesTheAttachment(t *testing.T) {
	fixture := newWorksFixture(t)
	category, root := fixture.createCategoryWithRoot(t, "Music", "/media/music")
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	target := "/api/v1/categories/" + strconv.FormatInt(category.ID, 10) +
		"/rootfolders/" + strconv.FormatInt(root.ID, 10)
	recorder := fixture.do(authedDelete(t, cookies, target))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}

	attached := fixture.cache.Snapshot().CategoriesForRootFolder(root.ID)
	if len(attached) != 0 {
		t.Errorf("CategoriesForRootFolder = %v, want none after detach", attached)
	}
}

// A library added as a kind starts with the kind's profiles and language, and one of no kind its type has is refused.
func TestCreateCategory_AKindBringsItsDefaults(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/categories", `{"media_type":"tv","name":"Anime","kind":"anime"}`))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var created struct {
		ID   int64  `json:"id"`
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil || created.Kind != "anime" {
		t.Fatalf("created = %+v, %v; want kind anime", created, err)
	}
	snapshot := fixture.cache.Snapshot()
	category, _ := snapshot.Category(created.ID)
	var names []string
	for _, id := range category.QualityProfileIDs {
		profile, _ := snapshot.QualityProfile(id)
		names = append(names, profile.Name)
	}
	if !reflect.DeepEqual(names, []string{"Anime 1080p", "WEB 1080p"}) {
		t.Errorf("profiles = %v, want the kind's, best first, the unknown one left out", names)
	}
	if category.LanguageProfileID == nil {
		t.Fatalf("no language profile, want the kind's")
	}
	language, _ := snapshot.LanguageProfile(*category.LanguageProfileID)
	if language.Name != "Japanese first" || !reflect.DeepEqual(language.AudioPreferred, []string{"ja", "en"}) {
		t.Errorf("language profile = %+v, want the kind's", language)
	}

	defaulted := fixture.do(authedPost(t, cookies, "/api/v1/categories", `{"media_type":"tv","name":"Shows"}`))
	if err := json.Unmarshal(defaulted.Body.Bytes(), &created); err != nil || created.Kind != "series" {
		t.Errorf("no kind given: %+v, %v; want the type's first, series", created, err)
	}
	if refused := fixture.do(authedPost(t, cookies, "/api/v1/categories", `{"media_type":"tv","name":"Odd","kind":"opera"}`)); refused.Code != http.StatusBadRequest {
		t.Errorf("an unknown kind: status %d, want 400", refused.Code)
	}
}
