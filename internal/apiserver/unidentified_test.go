package apiserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/makearr/MediaNexus/internal/apiserver"
	"github.com/makearr/MediaNexus/internal/paginate"
	"github.com/makearr/MediaNexus/internal/settings"
)

// fakeUnidentifiedReader mirrors fakeWorksReader: it records the root folder ids
// it was called with and answers canned rows, so a test can assert what category
// visibility computed without a real library.db.
type fakeUnidentifiedReader struct {
	rows              []apiserver.UnidentifiedRow
	lastRootFolderIDs []int64
	calls             int
}

func (f *fakeUnidentifiedReader) ListUnidentified(
	_ context.Context, rootFolderIDs []int64, _ paginate.Cursor, pageSize int,
) ([]apiserver.UnidentifiedRow, error) {
	f.calls++
	f.lastRootFolderIDs = rootFolderIDs
	if len(f.rows) > pageSize {
		return f.rows[:pageSize], nil
	}
	return f.rows, nil
}

// The core guarantee, same as TestListWorks_ExcludesRootFoldersOfCategoriesNotGranted:
// a readonly user with access to only one category must never have the other
// category's root folder reach the library query.
func TestListUnidentified_ExcludesRootFoldersOfCategoriesNotGranted(t *testing.T) {
	fixture := newWorksFixture(t)
	visible, rootVisible := fixture.createCategoryWithRoot(t, "Music", "/media/music")
	_, rootHidden := fixture.createCategoryWithRoot(t, "Private Stash", "/media/private")
	user := fixture.createUser(t, "listener", settings.RoleReadonly)
	if err := fixture.cache.SetCategoryAccess(t.Context(), user.ID, visible.ID, settings.PermissionRead); err != nil {
		t.Fatalf("SetCategoryAccess: %v", err)
	}
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/library/unidentified"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}

	got := fixture.unidentified.lastRootFolderIDs
	if slices.Contains(got, rootHidden.ID) {
		t.Errorf("root folder ids = %v, must not include the ungranted category's root %d", got, rootHidden.ID)
	}
	if !slices.Contains(got, rootVisible.ID) {
		t.Errorf("root folder ids = %v, must include the granted category's root %d", got, rootVisible.ID)
	}
}

// A user granted nothing must produce a non-nil empty filter -- nil means
// unfiltered, which here would show every category's queue to someone who can
// see none of them.
func TestListUnidentified_NoGrantsProducesNonNilEmptyFilter(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createCategoryWithRoot(t, "Music", "/media/music")
	fixture.createUser(t, "nobody", settings.RoleReadonly)
	cookies := fixture.signIn(t, "nobody")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/library/unidentified"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}

	got := fixture.unidentified.lastRootFolderIDs
	if got == nil {
		t.Fatal("root folder ids is nil, want a non-nil empty slice -- nil is unfiltered")
	}
	if len(got) != 0 {
		t.Errorf("root folder ids = %v, want empty: this user has no category grants", got)
	}
}

// Invariant 37, same guard as /works: naming a category the caller cannot read
// answers 404, never 403 and never 200 with an unfiltered list.
func TestListUnidentified_CategoryIDForAnUngrantedCategoryAnswers404(t *testing.T) {
	fixture := newWorksFixture(t)
	_, _ = fixture.createCategoryWithRoot(t, "Music", "/media/music")
	hidden, _ := fixture.createCategoryWithRoot(t, "Private Stash", "/media/private")
	user := fixture.createUser(t, "listener", settings.RoleReadonly)
	if err := fixture.cache.SetCategoryAccess(t.Context(), user.ID, hidden.ID, settings.PermissionNone); err != nil {
		t.Fatalf("SetCategoryAccess: %v", err)
	}
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/library/unidentified?category_id="+itoa(hidden.ID)))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a category this caller cannot read", recorder.Code)
	}
}

func TestListUnidentified_ResponseShapeAndCursor(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createCategoryWithRoot(t, "Music", "/media/music")
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	fixture.unidentified.rows = []apiserver.UnidentifiedRow{
		{
			MediaObjectID: 1, RootFolderID: 1, RelPath: "Unsorted/one.mp3",
			Candidates:  []apiserver.UnidentifiedCandidate{{Strategy: "embedded-tag", Confidence: 0.4}},
			FirstSeenAt: fixture.clock.Now(), LastSeenAt: fixture.clock.Now(),
		},
		{
			MediaObjectID: 2, RootFolderID: 1, RelPath: "Unsorted/two.mp3",
			FirstSeenAt: fixture.clock.Now(), LastSeenAt: fixture.clock.Now(),
		},
	}

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/library/unidentified?limit=1"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}

	var body struct {
		Items []struct {
			MediaObjectID int64  `json:"media_object_id"`
			RelPath       string `json:"rel_path"`
			Candidates    []struct {
				Strategy   string  `json:"strategy"`
				Confidence float64 `json:"confidence"`
			} `json:"candidates"`
		} `json:"items"`
		NextCursor string `json:"next_cursor"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].RelPath != "Unsorted/one.mp3" {
		t.Fatalf("items = %+v, want exactly the first row (limit=1)", body.Items)
	}
	if len(body.Items[0].Candidates) != 1 || body.Items[0].Candidates[0].Strategy != "embedded-tag" {
		t.Errorf("candidates = %+v, want the one queued candidate", body.Items[0].Candidates)
	}
	if body.NextCursor == "" {
		t.Error("NextCursor is empty, want a cursor: there is a second row beyond the limit")
	}
}
