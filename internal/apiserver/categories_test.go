package apiserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/makearr/MediaNexus/internal/settings"
)

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
