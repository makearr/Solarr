package apiserver_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
)

type importListBody struct {
	ID           int64  `json:"id"`
	CategoryID   int64  `json:"category_id"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	CadenceHours int    `json:"cadence_hours"`
	Monitor      bool   `json:"monitor"`
	Enabled      bool   `json:"enabled"`
}

func TestCreateImportList_AddsOneAdministratorsCanSeeImmediately(t *testing.T) {
	fixture := newWorksFixture(t)
	category, err := fixture.cache.CreateCategory(t.Context(), "music", "Music", 100)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies,
		fmt.Sprintf("/api/v1/libraries/%d/import-lists", category.ID),
		`{"name":"Favourites","type":"watchlist","cadence_hours":24}`))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var created importListBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if created.Name != "Favourites" || created.CategoryID != category.ID || created.CadenceHours != 24 {
		t.Errorf("created = %+v", created)
	}

	listRecorder := fixture.do(authedGet(t, cookies, fmt.Sprintf("/api/v1/libraries/%d/import-lists", category.ID)))
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", listRecorder.Code, listRecorder.Body)
	}
	var listed []importListBody
	if err := json.Unmarshal(listRecorder.Body.Bytes(), &listed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != created.ID {
		t.Errorf("listed = %+v, want exactly the one created", listed)
	}
}

func TestCreateImportList_RequiresAdministrator(t *testing.T) {
	fixture := newWorksFixture(t)
	category, err := fixture.cache.CreateCategory(t.Context(), "music", "Music", 100)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedPost(t, cookies,
		fmt.Sprintf("/api/v1/libraries/%d/import-lists", category.ID),
		`{"name":"Favourites","type":"watchlist","cadence_hours":24}`))
	if recorder.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for a non-administrator", recorder.Code)
	}
}

func TestCreateImportList_InvalidInputAnswers400(t *testing.T) {
	fixture := newWorksFixture(t)
	category, err := fixture.cache.CreateCategory(t.Context(), "music", "Music", 100)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies,
		fmt.Sprintf("/api/v1/libraries/%d/import-lists", category.ID),
		`{"name":"","type":"watchlist","cadence_hours":24}`))
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for an empty name, body = %s", recorder.Code, recorder.Body)
	}
}

// A category the caller cannot read must not leak its import lists.
func TestListImportLists_UngrantedCategoryAnswers404(t *testing.T) {
	fixture := newWorksFixture(t)
	category, err := fixture.cache.CreateCategory(t.Context(), "music", "Music", 100)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedGet(t, cookies, fmt.Sprintf("/api/v1/libraries/%d/import-lists", category.ID)))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", recorder.Code)
	}
}

func TestUpdateImportList_ChangesTheStoredFields(t *testing.T) {
	fixture := newWorksFixture(t)
	category, err := fixture.cache.CreateCategory(t.Context(), "music", "Music", 100)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	created, err := fixture.cache.CreateImportList(t.Context(), category.ID, settings.ImportListRequest{
		Name: "Favourites", Type: "watchlist", CadenceHours: 24, Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateImportList: %v", err)
	}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPatch(t, cookies,
		"/api/v1/import-lists/"+strconv.FormatInt(created.ID, 10),
		`{"name":"Favourites","type":"watchlist","cadence_hours":12,"enabled":false}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var updated importListBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &updated); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if updated.CadenceHours != 12 || updated.Enabled {
		t.Errorf("updated = %+v, want cadence_hours=12 enabled=false", updated)
	}
}

func TestDeleteImportList_RemovesIt(t *testing.T) {
	fixture := newWorksFixture(t)
	category, err := fixture.cache.CreateCategory(t.Context(), "music", "Music", 100)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	created, err := fixture.cache.CreateImportList(t.Context(), category.ID, settings.ImportListRequest{
		Name: "Favourites", Type: "watchlist", CadenceHours: 24,
	})
	if err != nil {
		t.Fatalf("CreateImportList: %v", err)
	}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedDelete(t, cookies, "/api/v1/import-lists/"+strconv.FormatInt(created.ID, 10)))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}

	lists, err := fixture.cache.ImportLists(t.Context(), category.ID)
	if err != nil {
		t.Fatalf("ImportLists: %v", err)
	}
	if len(lists) != 0 {
		t.Errorf("ImportLists = %+v, want none after delete", lists)
	}
}

func TestDeleteImportList_RequiresAdministrator(t *testing.T) {
	fixture := newWorksFixture(t)
	category, err := fixture.cache.CreateCategory(t.Context(), "music", "Music", 100)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	created, err := fixture.cache.CreateImportList(t.Context(), category.ID, settings.ImportListRequest{
		Name: "Favourites", Type: "watchlist", CadenceHours: 24,
	})
	if err != nil {
		t.Fatalf("CreateImportList: %v", err)
	}
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedDelete(t, cookies, "/api/v1/import-lists/"+strconv.FormatInt(created.ID, 10)))
	if recorder.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for a non-administrator", recorder.Code)
	}
}
