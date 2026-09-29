package apiserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
)

// fakeDiscover records what it was called with and answers canned results,
// the same shape fakeWorksMonitor already uses.
type fakeDiscover struct {
	lastAddSearch bool
	results       []apiserver.DiscoverResult
	add           apiserver.DiscoverAddResult
	err           error

	lastSearchCategoryID int64
	lastSearchQuery      string
	lastSearchKind       apiserver.DiscoverKind
	lastSearchLimit      int
	searchCalls          int

	lastAddCategoryID int64
	lastAddUserID     int64
	lastAddProvider   string
	lastAddExternalID string
	lastAddMonitored  bool
	addCalls          int
}

func (f *fakeDiscover) Search(
	_ context.Context, categoryID int64, kind apiserver.DiscoverKind, query string, limit int,
) ([]apiserver.DiscoverResult, error) {
	f.searchCalls++
	f.lastSearchCategoryID, f.lastSearchQuery, f.lastSearchLimit = categoryID, query, limit
	f.lastSearchKind = kind
	if f.err != nil {
		return nil, f.err
	}
	return f.results, nil
}

func (f *fakeDiscover) Add(
	_ context.Context, categoryID, userID int64, provider, externalID string, monitored, search bool,
) (apiserver.DiscoverAddResult, error) {
	f.addCalls++
	f.lastAddSearch = search
	f.lastAddCategoryID, f.lastAddProvider, f.lastAddExternalID, f.lastAddMonitored =
		categoryID, provider, externalID, monitored
	f.lastAddUserID = userID
	if f.err != nil {
		return apiserver.DiscoverAddResult{}, f.err
	}
	return f.add, nil
}

func TestDiscoverSearch_ReachesTheFilterAndRendersResults(t *testing.T) {
	fixture := newWorksFixture(t)
	category, _ := fixture.createCategoryWithRoot(t, "Music", "/media/music")
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	year := int64(1997)
	fixture.discover.results = []apiserver.DiscoverResult{
		{Provider: "musicbrainz:release-group", ExternalID: "rg-1", Title: "KI Pecezinu", Artist: "Katupoken", Year: &year},
	}

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/discover/search?category_id="+
		itoa(category.ID)+"&q=ki+pecezinu"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}
	if fixture.discover.lastSearchCategoryID != category.ID || fixture.discover.lastSearchQuery != "ki pecezinu" {
		t.Errorf("Search called with category=%d query=%q, want %d/%q",
			fixture.discover.lastSearchCategoryID, fixture.discover.lastSearchQuery, category.ID, "ki pecezinu")
	}
	if recorder.Body.String() == "" || recorder.Body.String() == "null" {
		t.Errorf("body = %q, want the rendered results", recorder.Body.String())
	}
}

func TestDiscoverSearch_RequiresCategoryID(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/discover/search?q=ki+pecezinu"))
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 without category_id", recorder.Code)
	}
}

func TestDiscoverSearch_UngrantedCategoryAnswers404(t *testing.T) {
	fixture := newWorksFixtureWithLibrarySize(t, nil)
	category, _ := fixture.createCategoryWithRoot(t, "Music", "/media/music")
	fixture.createUser(t, "viewer", settings.RoleReadonly)
	cookies := fixture.signIn(t, "viewer")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/discover/search?category_id="+
		itoa(category.ID)+"&q=ki+pecezinu"))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a category with no grant (Invariant 37)", recorder.Code)
	}
	if fixture.discover.searchCalls != 0 {
		t.Errorf("Search called %d times, want 0 -- the permission check must run first", fixture.discover.searchCalls)
	}
}

func TestDiscoverAdd_ReachesTheAdderAndRendersTheResult(t *testing.T) {
	fixture := newWorksFixture(t)
	category, _ := fixture.createCategoryWithRoot(t, "Music", "/media/music")
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	fixture.discover.add = apiserver.DiscoverAddResult{WorkID: 42, DesiredItemID: 7, Priority: 1234}

	body := `{"category_id":` + itoa(category.ID) + `,"provider":"musicbrainz:release-group","external_id":"rg-1"}`
	recorder := fixture.do(authedPost(t, cookies, "/api/v1/discover/add", body))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}
	if fixture.discover.lastAddCategoryID != category.ID || fixture.discover.lastAddExternalID != "rg-1" {
		t.Errorf("Add called with category=%d external_id=%q, want %d/rg-1",
			fixture.discover.lastAddCategoryID, fixture.discover.lastAddExternalID, category.ID)
	}
	if !fixture.discover.lastAddMonitored {
		t.Error("Monitored = false, want true (the request omitted it, default true)")
	}
}

// A viewer with only read access must not be able to add -- Manage, not Read,
// is what Add checks (discover.go's own comment on why).
func TestDiscoverAdd_ReadOnlyGrantIsRefused(t *testing.T) {
	fixture := newWorksFixtureWithLibrarySize(t, nil)
	category, _ := fixture.createCategoryWithRoot(t, "Music", "/media/music")
	viewer := fixture.createUser(t, "viewer", settings.RoleReadonly)
	if err := fixture.cache.SetCategoryAccess(t.Context(), viewer.ID, category.ID, settings.PermissionRead); err != nil {
		t.Fatalf("SetCategoryAccess: %v", err)
	}
	cookies := fixture.signIn(t, "viewer")

	body := `{"category_id":` + itoa(category.ID) + `,"provider":"musicbrainz:release-group","external_id":"rg-1"}`
	recorder := fixture.do(authedPost(t, cookies, "/api/v1/discover/add", body))
	if recorder.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for a read-only grant", recorder.Code)
	}
	if fixture.discover.addCalls != 0 {
		t.Errorf("Add called %d times, want 0", fixture.discover.addCalls)
	}
}

func TestDiscoverAdd_MissingExternalIDAnswers400(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/discover/add", `{"category_id":1,"provider":"musicbrainz:release-group"}`))
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 without external_id", recorder.Code)
	}
}

// kind reaches the engine, so an artist search is not silently an album one.
func TestDiscoverSearch_PassesTheKindThrough(t *testing.T) {
	fixture := newWorksFixture(t)
	category, _ := fixture.createCategoryWithRoot(t, "Music", "/media/music")
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	path := fmt.Sprintf("/api/v1/discover/search?category_id=%d&q=heka&kind=artist", category.ID)
	if recorder := fixture.do(authedGet(t, cookies, path)); recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if fixture.discover.lastSearchKind != apiserver.DiscoverArtist {
		t.Errorf("lastSearchKind = %q, want artist", fixture.discover.lastSearchKind)
	}
}

// Omitting kind keeps the behaviour this endpoint had before artists were
// searchable, so an older client is unaffected.
func TestDiscoverSearch_DefaultsToAlbum(t *testing.T) {
	fixture := newWorksFixture(t)
	category, _ := fixture.createCategoryWithRoot(t, "Music", "/media/music")
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	path := fmt.Sprintf("/api/v1/discover/search?category_id=%d&q=heka", category.ID)
	fixture.do(authedGet(t, cookies, path))
	if fixture.discover.lastSearchKind != apiserver.DiscoverAlbum {
		t.Errorf("lastSearchKind = %q, want album", fixture.discover.lastSearchKind)
	}
}

func TestDiscoverSearch_RejectsAnUnknownKind(t *testing.T) {
	fixture := newWorksFixture(t)
	category, _ := fixture.createCategoryWithRoot(t, "Music", "/media/music")
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	path := fmt.Sprintf("/api/v1/discover/search?category_id=%d&q=heka&kind=composer", category.ID)
	recorder := fixture.do(authedGet(t, cookies, path))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", recorder.Code, recorder.Body)
	}
	if fixture.discover.searchCalls != 0 {
		t.Error("an unknown kind must be refused before it reaches the engine")
	}
}

// An artist add reports person_id and subscription_id, and does not report
// work_id or desired_item_id -- a client must never read a zero as a real row.
func TestDiscoverAdd_ArtistReportsOnlyItsOwnIDs(t *testing.T) {
	fixture := newWorksFixture(t)
	category, _ := fixture.createCategoryWithRoot(t, "Music", "/media/music")
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	fixture.discover.add = apiserver.DiscoverAddResult{
		Kind: apiserver.DiscoverArtist, PersonID: 42, SubscriptionID: 7, Priority: 100,
	}
	body := fmt.Sprintf(
		`{"category_id":%d,"provider":"musicbrainz:artist","external_id":"artist-1"}`, category.ID)
	recorder := fixture.do(authedPost(t, cookies, "/api/v1/discover/add", body))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}

	var decoded map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded["kind"] != "artist" {
		t.Errorf("kind = %v, want artist", decoded["kind"])
	}
	if decoded["person_id"] != float64(42) || decoded["subscription_id"] != float64(7) {
		t.Errorf("ids = %v/%v, want 42/7", decoded["person_id"], decoded["subscription_id"])
	}
	if _, present := decoded["work_id"]; present {
		t.Error("work_id must be absent for an artist add, not zero")
	}
	if _, present := decoded["desired_item_id"]; present {
		t.Error("desired_item_id must be absent for an artist add, not zero")
	}
}

// The subscription is the calling user's, so the user id has to reach the
// engine rather than being defaulted there.
func TestDiscoverAdd_PassesTheCallingUser(t *testing.T) {
	fixture := newWorksFixture(t)
	category, _ := fixture.createCategoryWithRoot(t, "Music", "/media/music")
	user := fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	body := fmt.Sprintf(
		`{"category_id":%d,"provider":"musicbrainz:artist","external_id":"artist-1"}`, category.ID)
	fixture.do(authedPost(t, cookies, "/api/v1/discover/add", body))
	if fixture.discover.lastAddUserID != user.ID {
		t.Errorf("lastAddUserID = %d, want the signed-in user %d", fixture.discover.lastAddUserID, user.ID)
	}
}
