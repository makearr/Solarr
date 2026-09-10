package apiserver_test

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/makearr/MediaNexus/internal/api/apiserver"
	"github.com/makearr/MediaNexus/internal/db/settings"
)

// fakeArtworkReader mirrors fakeWorkDetailReader: records the id it was called
// with and answers a canned row, without a real library.db.
type fakeArtworkReader struct {
	row    apiserver.WorkArtworkRow
	err    error
	lastID int64
}

func (f *fakeArtworkReader) GetWorkArtwork(_ context.Context, id int64) (apiserver.WorkArtworkRow, error) {
	f.lastID = id
	if f.err != nil {
		return apiserver.WorkArtworkRow{}, f.err
	}
	return f.row, nil
}

// writeFixtureImage puts bytes at <testImagesDir>/<cacheKey>-<size>.webp in the
// fixture's in-memory filesystem, the same layout ArtworkJob writes on disk.
func (f worksFixture) writeFixtureImage(t *testing.T, cacheKey, size string, content []byte) {
	t.Helper()
	path := filepath.Join(testImagesDir, cacheKey+"-"+size+".webp")
	if err := f.fsys.MkdirAll(testImagesDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	file, err := f.fsys.Create(path)
	if err != nil {
		t.Fatalf("Create(%q): %v", path, err)
	}
	if _, err := file.Write(content); err != nil {
		t.Fatalf("Write(%q): %v", path, err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close(%q): %v", path, err)
	}
}

func TestGetWorkArtwork_ServesTheCachedImage(t *testing.T) {
	fixture := newWorksFixture(t)
	visible, rootVisible := fixture.createCategoryWithRoot(t, "Music", "/media/music")
	user := fixture.createUser(t, "listener", settings.RoleReadonly)
	if err := fixture.cache.SetCategoryAccess(t.Context(), user.ID, visible.ID, settings.PermissionRead); err != nil {
		t.Fatalf("SetCategoryAccess: %v", err)
	}
	cookies := fixture.signIn(t, "listener")

	fixture.artwork.row = apiserver.WorkArtworkRow{
		CacheKey: "work-1-cover", Found: true, RootFolderIDs: []int64{rootVisible.ID},
	}
	fixture.writeFixtureImage(t, "work-1-cover", "small", []byte("fake webp bytes"))

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/works/1/artwork"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}
	if got := recorder.Header().Get("Content-Type"); got != "image/webp" {
		t.Errorf("Content-Type = %q, want image/webp", got)
	}
	if recorder.Body.String() != "fake webp bytes" {
		t.Errorf("body = %q, want the cached image bytes", recorder.Body.String())
	}
	if fixture.artwork.lastID != 1 {
		t.Errorf("GetWorkArtwork called with id %d, want 1", fixture.artwork.lastID)
	}
}

func TestGetWorkArtwork_SizeParameterSelectsTheFile(t *testing.T) {
	fixture := newWorksFixture(t)
	visible, rootVisible := fixture.createCategoryWithRoot(t, "Music", "/media/music")
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	fixture.artwork.row = apiserver.WorkArtworkRow{
		CacheKey: "work-2-cover", Found: true, RootFolderIDs: []int64{rootVisible.ID},
	}
	fixture.writeFixtureImage(t, "work-2-cover", "full", []byte("the full-size image"))
	_ = visible

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/works/2/artwork?size=full"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}
	if recorder.Body.String() != "the full-size image" {
		t.Errorf("body = %q, want the full-size file, not the default small one", recorder.Body.String())
	}
}

func TestGetWorkArtwork_InvalidSizeAnswers400(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/works/1/artwork?size=huge"))
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for an unrecognised size", recorder.Code)
	}
}

func TestGetWorkArtwork_NoCoverRecordedAnswers404(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	fixture.artwork.row = apiserver.WorkArtworkRow{Found: false}

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/works/1/artwork"))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when no cover has been recorded yet", recorder.Code)
	}
}

// Invariant 37: a work under a root folder the caller cannot read must answer
// 404 for its artwork too, indistinguishable from no cover recorded at all --
// the same guarantee GetWork already has for the work's own detail.
func TestGetWorkArtwork_UnderARootFolderTheCallerCannotReadAnswers404(t *testing.T) {
	fixture := newWorksFixture(t)
	_, rootHidden := fixture.createCategoryWithRoot(t, "Private Stash", "/media/private")
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	fixture.artwork.row = apiserver.WorkArtworkRow{
		CacheKey: "work-1-cover", Found: true, RootFolderIDs: []int64{rootHidden.ID},
	}
	fixture.writeFixtureImage(t, "work-1-cover", "small", []byte("secret cover"))

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/works/1/artwork"))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a work this caller cannot read", recorder.Code)
	}
}

func TestGetWorkArtwork_MalformedIDAnswers400(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/works/not-a-number/artwork"))
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a non-numeric id", recorder.Code)
	}
}
