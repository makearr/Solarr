package apiserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
)

// fakeSearchReader records the filter it was called with and answers canned rows, so a
// test can assert what category visibility computed without a real title_search index.
type fakeSearchReader struct {
	rows       []apiserver.SearchResultRow
	err        error
	lastFilter apiserver.SearchFilter
	lastLimit  int
	calls      int
}

func (f *fakeSearchReader) SearchWorks(
	_ context.Context, filter apiserver.SearchFilter, limit int,
) ([]apiserver.SearchResultRow, error) {
	f.calls++
	f.lastFilter = filter
	f.lastLimit = limit
	if f.err != nil {
		return nil, f.err
	}
	return f.rows, nil
}

// newSearchFixture reuses worksFixture's settings.db wiring (categories, root folders,
// access grants) and helper methods -- newWorksFixture already wires a fakeSearchReader
// alongside the works one.
func newSearchFixture(t *testing.T) (worksFixture, *fakeSearchReader) {
	t.Helper()
	fixture := newWorksFixture(t)
	return fixture, fixture.search
}

func TestSearch_QueryBelowMinimumLengthAnswers400(t *testing.T) {
	fixture, search := newSearchFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	search.err = apiserver.ErrQueryTooShort

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/search?q=a"))
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a query below the 2-character floor", recorder.Code)
	}
}

// The core guarantee, mirroring TestListWorks_ExcludesRootFoldersOfCategoriesNotGranted:
// a readonly user with access to only one category must never have the other category's
// root folder reach the search query.
func TestSearch_ExcludesRootFoldersOfCategoriesNotGranted(t *testing.T) {
	fixture, search := newSearchFixture(t)
	visible, rootVisible := fixture.createCategoryWithRoot(t, "Music", "/media/music")
	_, rootHidden := fixture.createCategoryWithRoot(t, "Private Stash", "/media/private")
	user := fixture.createUser(t, "listener", settings.RoleReadonly)
	if err := fixture.cache.SetCategoryAccess(t.Context(), user.ID, visible.ID, settings.PermissionRead); err != nil {
		t.Fatalf("SetCategoryAccess: %v", err)
	}
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/search?q=amber"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}

	got := search.lastFilter.RootFolderIDs
	if slices.Contains(got, rootHidden.ID) {
		t.Errorf("RootFolderIDs = %v, must not include the ungranted category's root %d", got, rootHidden.ID)
	}
	if !slices.Contains(got, rootVisible.ID) {
		t.Errorf("RootFolderIDs = %v, must include the granted category's root %d", got, rootVisible.ID)
	}
}

// A user granted nothing must produce a non-nil empty filter -- mirroring
// TestListWorks_NoGrantsProducesNonNilEmptyFilter, and the same trap: nil means
// unfiltered (library.Search.Works' own distinction), which here would search every
// category for someone who can see none of them. Mutation-tested: forcing
// visibleRootFolders to return nil here instead of an empty slice makes this fail.
func TestSearch_NoGrantsProducesNonNilEmptyFilter(t *testing.T) {
	fixture, search := newSearchFixture(t)
	fixture.createCategoryWithRoot(t, "Music", "/media/music")
	fixture.createUser(t, "nobody", settings.RoleReadonly)
	cookies := fixture.signIn(t, "nobody")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/search?q=amber"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}

	got := search.lastFilter.RootFolderIDs
	if got == nil {
		t.Fatal("RootFolderIDs is nil, want a non-nil empty slice -- nil is unfiltered")
	}
	if len(got) != 0 {
		t.Errorf("RootFolderIDs = %v, want empty: this user has no category grants", got)
	}
}

func TestSearch_NoMatchesReturnsEmptyArrayNotError(t *testing.T) {
	fixture, search := newSearchFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	search.rows = nil

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/search?q=nothing+matches+this"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}
	if got := recorder.Body.String(); got != "[]\n" {
		t.Errorf("body = %q, want an empty JSON array, not null", got)
	}
}

func TestSearch_ResultsComeBackRankedBestFirst(t *testing.T) {
	fixture, search := newSearchFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	search.rows = []apiserver.SearchResultRow{
		{Work: apiserver.WorkRow{ID: 1, MediaType: "music", Title: "Boatman's Call", SortTitle: "boatmans call",
			AddedAt: fixture.clock.Now(), UpdatedAt: fixture.clock.Now()}, Score: 0.1},
		{Work: apiserver.WorkRow{ID: 2, MediaType: "music", Title: "Something Else", SortTitle: "something else",
			AddedAt: fixture.clock.Now(), UpdatedAt: fixture.clock.Now()}, Score: 0.9},
	}

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/search?q=boatman"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}

	var body []struct {
		Work struct {
			ID    int64  `json:"id"`
			Title string `json:"title"`
		} `json:"work"`
		Score float64 `json:"score"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 2 {
		t.Fatalf("got %d results, want 2: %+v", len(body), body)
	}
	if body[0].Work.ID != 1 || body[0].Work.Title != "Boatman's Call" {
		t.Errorf("first result = %+v, want the lower-score (better) match leading", body[0])
	}
	if body[1].Work.ID != 2 {
		t.Errorf("second result = %+v, want the higher-score match second", body[1])
	}
}

func TestSearch_LimitAndMediaTypeReachTheReader(t *testing.T) {
	fixture, search := newSearchFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/search?q=amber&media_type=music&limit=5"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}
	if search.lastFilter.Query != "amber" {
		t.Errorf("Query = %q, want %q", search.lastFilter.Query, "amber")
	}
	if search.lastFilter.MediaType != "music" {
		t.Errorf("MediaType = %q, want %q", search.lastFilter.MediaType, "music")
	}
	if search.lastLimit != 5 {
		t.Errorf("limit = %d, want 5", search.lastLimit)
	}
}

// A limit above searchMaxLimit must clamp down: the search box is sized for
// interactive use, not a bulk export.
func TestSearch_LimitClampsToTheInteractiveMax(t *testing.T) {
	fixture, search := newSearchFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/search?q=amber&limit=1000"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}
	if search.lastLimit > 50 {
		t.Errorf("limit = %d, want clamped to the search-box max", search.lastLimit)
	}
}

func TestSearch_UnauthenticatedAnswers401(t *testing.T) {
	fixture, _ := newSearchFixture(t)

	recorder := fixture.do(authedGet(t, nil, "/api/v1/search?q=amber"))
	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", recorder.Code)
	}
}
