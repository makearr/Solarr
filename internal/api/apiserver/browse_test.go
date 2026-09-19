package apiserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
)

type pathListingBody struct {
	Path      string `json:"path"`
	Parent    string `json:"parent"`
	Truncated bool   `json:"truncated"`
	Entries   []struct {
		Name  string `json:"name"`
		Path  string `json:"path"`
		IsDir bool   `json:"is_dir"`
	} `json:"entries"`
}

func browseFixture(t *testing.T) (worksFixture, []*http.Cookie) {
	t.Helper()
	fixture := newWorksFixture(t)
	for _, directory := range []string{"/storage/media/music", "/storage/media/movies", "/storage/downloads"} {
		if err := fixture.fsys.MkdirAll(directory, 0o755); err != nil {
			t.Fatalf("MkdirAll %s: %v", directory, err)
		}
	}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	return fixture, fixture.signIn(t, "admin")
}

func browse(t *testing.T, fixture worksFixture, cookies []*http.Cookie, query string) pathListingBody {
	t.Helper()
	recorder := fixture.do(authedGet(t, cookies, "/api/v1/storage/browse"+query))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var listing pathListingBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &listing); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return listing
}

func TestBrowsePath_ListsADirectory(t *testing.T) {
	fixture, cookies := browseFixture(t)

	listing := browse(t, fixture, cookies, "?path=/storage/media")
	if listing.Path != "/storage/media" || listing.Parent != "/storage" {
		t.Errorf("listing = %+v, want /storage/media below /storage", listing)
	}
	if len(listing.Entries) != 2 {
		t.Fatalf("entries = %+v, want music and movies", listing.Entries)
	}
	if listing.Entries[0].Name != "movies" || listing.Entries[0].Path != "/storage/media/movies" {
		t.Errorf("first entry = %+v, want movies sorted first", listing.Entries[0])
	}
	if !listing.Entries[0].IsDir {
		t.Errorf("first entry = %+v, want a directory", listing.Entries[0])
	}
}

// A half-typed path is the typeahead case: the nearest real directory above
// it is listed, filtered by what has been typed.
func TestBrowsePath_FiltersAPartialPathByPrefix(t *testing.T) {
	fixture, cookies := browseFixture(t)

	listing := browse(t, fixture, cookies, "?path=/storage/media/mu")
	if listing.Path != "/storage/media" {
		t.Errorf("path = %q, want the directory above the partial name", listing.Path)
	}
	if len(listing.Entries) != 1 || listing.Entries[0].Name != "music" {
		t.Errorf("entries = %+v, want only music", listing.Entries)
	}
}

func TestBrowsePath_RootHasNoParent(t *testing.T) {
	fixture, cookies := browseFixture(t)

	listing := browse(t, fixture, cookies, "")
	if listing.Path != "/" || listing.Parent != "" {
		t.Errorf("listing = %+v, want the root with no parent", listing)
	}
}

func TestBrowsePath_RefusesANonAdministrator(t *testing.T) {
	fixture, _ := browseFixture(t)
	fixture.createUser(t, "viewer", settings.RoleReadonly)
	cookies := fixture.signIn(t, "viewer")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/storage/browse?path=/storage"))
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", recorder.Code)
	}
}

func TestBrowsePath_AnsweredNotFoundForAMissingDirectory(t *testing.T) {
	fixture, cookies := browseFixture(t)

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/storage/browse?path=/nowhere/at/all"))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
}
