package apiserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
)

// fakeWorkAdder records what it was called with and answers canned results.
type fakeWorkAdder struct {
	candidates []apiserver.AddCandidateRow
	result     apiserver.AddWorkResult

	lookups    int
	lastLookup [3]string
	adds       []apiserver.AddWorkRequest
}

func (f *fakeWorkAdder) Lookup(_ context.Context, categoryID int64, kind, query string, _ int) ([]apiserver.AddCandidateRow, error) {
	f.lookups++
	f.lastLookup = [3]string{fmt.Sprint(categoryID), kind, query}
	return f.candidates, nil
}

func (f *fakeWorkAdder) Add(_ context.Context, request apiserver.AddWorkRequest) (apiserver.AddWorkResult, error) {
	f.adds = append(f.adds, request)
	return f.result, nil
}

// A lookup reaches the library's adder with its kind and query and renders what it found; an empty kind is the
// type's default, decided by the adder.
func TestLookupWorks_ReachesTheAdderAndRendersCandidates(t *testing.T) {
	fixture := newWorksFixture(t)
	category, _ := fixture.createCategoryWithRoot(t, "Music", "/media/music")
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	fixture.workAdder.candidates = []apiserver.AddCandidateRow{{Kind: "album", Provider: "p", ExternalID: "rg-1", Title: "KI Pecezinu", Year: 1997, LibraryID: 9}}

	recorder := fixture.do(authedGet(t, cookies, fmt.Sprintf("/api/v1/works/lookup?category_id=%d&q=ki+pecezinu&kind=artist", category.ID)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}
	if want := [3]string{fmt.Sprint(category.ID), "artist", "ki pecezinu"}; fixture.workAdder.lastLookup != want {
		t.Errorf("lookup = %v, want %v", fixture.workAdder.lastLookup, want)
	}
	var got []map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := []map[string]any{{"kind": "album", "provider": "p", "external_id": "rg-1", "title": "KI Pecezinu", "year": float64(1997), "library_id": float64(9)}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("candidates = %v, want %v", got, want)
	}
}

// Read is needed to look up, manage to add; a library the caller cannot see is 404 and the adder is never reached.
func TestAddWork_ChecksTheLibraryBeforeTheAdder(t *testing.T) {
	fixture := newWorksFixture(t)
	category, _ := fixture.createCategoryWithRoot(t, "Music", "/media/music")
	viewer := fixture.createUser(t, "viewer", settings.RoleReadonly)
	cookies := fixture.signIn(t, "viewer")
	body := fmt.Sprintf(`{"category_id":%d,"provider":"p","external_id":"rg-1"}`, category.ID)

	if recorder := fixture.do(authedGet(t, cookies, fmt.Sprintf("/api/v1/works/lookup?category_id=%d&q=heka", category.ID))); recorder.Code != http.StatusNotFound {
		t.Errorf("lookup without a grant: %d, want 404", recorder.Code)
	}
	if err := fixture.cache.SetCategoryAccess(t.Context(), viewer.ID, category.ID, settings.PermissionRead); err != nil {
		t.Fatal(err)
	}
	if recorder := fixture.do(authedPost(t, cookies, "/api/v1/works", body)); recorder.Code != http.StatusForbidden {
		t.Errorf("add with read: %d, want 403", recorder.Code)
	}
	if recorder := fixture.do(authedGet(t, cookies, "/api/v1/works/lookup?q=heka")); recorder.Code != http.StatusBadRequest {
		t.Errorf("lookup without category_id: %d, want 400", recorder.Code)
	}
	if fixture.workAdder.lookups != 0 || len(fixture.workAdder.adds) != 0 {
		t.Errorf("the adder was reached: %d lookups, %d adds", fixture.workAdder.lookups, len(fixture.workAdder.adds))
	}
}

// An add carries everything the dialog chose and the caller's own id; an artist's answer names no work.
func TestAddWork_PassesTheChoicesAndReportsOnlyItsOwnIDs(t *testing.T) {
	fixture := newWorksFixture(t)
	category, _ := fixture.createCategoryWithRoot(t, "TV", "/media/tv")
	user := fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	fixture.workAdder.result = apiserver.AddWorkResult{Kind: "artist", PersonID: 42, Searching: true}
	profile := int64(3)

	body := fmt.Sprintf(`{"category_id":%d,"kind":"series","provider":"tvdb","external_id":"81189","monitor_option":"future","specials":true,"quality_profile_id":3,"search":true}`, category.ID)
	recorder := fixture.do(authedPost(t, cookies, "/api/v1/works", body))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}
	want := apiserver.AddWorkRequest{
		CategoryID: category.ID, UserID: user.ID, Kind: "series", Provider: "tvdb", ExternalID: "81189", Monitored: true,
		MonitorOption: "future", Specials: true, ProfileID: &profile, Search: true,
	}
	if len(fixture.workAdder.adds) != 1 || !reflect.DeepEqual(fixture.workAdder.adds[0], want) {
		t.Errorf("add = %+v, want %+v", fixture.workAdder.adds, want)
	}
	var got map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if want := map[string]any{"kind": "artist", "person_id": float64(42), "searching": true}; !reflect.DeepEqual(got, want) {
		t.Errorf("result = %v, want %v (no zero work_id)", got, want)
	}
	if recorder := fixture.do(authedPost(t, cookies, "/api/v1/works", `{"category_id":1,"provider":"p"}`)); recorder.Code != http.StatusBadRequest {
		t.Errorf("without external_id: %d, want 400", recorder.Code)
	}
}
