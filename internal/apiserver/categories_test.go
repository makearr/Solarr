package apiserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"testing"

	"github.com/makearr/MediaNexus/internal/settings"
)

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
