package apiserver_test

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
)

// fakeArtworkReader mirrors fakeWorkDetailReader: records the id it was called
// with and answers a canned row, without a real library.db.
type fakeArtworkReader struct {
	row    apiserver.WorkArtworkRow
	err    error
	lastID int64

	personRow    apiserver.PersonArtworkRow
	personErr    error
	lastPersonID int64
}

func (f *fakeArtworkReader) GetWorkArtwork(_ context.Context, id int64) (apiserver.WorkArtworkRow, error) {
	f.lastID = id
	if f.err != nil {
		return apiserver.WorkArtworkRow{}, f.err
	}
	return f.row, nil
}

func (f *fakeArtworkReader) GetPersonArtwork(_ context.Context, id int64) (apiserver.PersonArtworkRow, error) {
	f.lastPersonID = id
	if f.personErr != nil {
		return apiserver.PersonArtworkRow{}, f.personErr
	}
	return f.personRow, nil
}

// fakeRemoteImages answers the cache key of each provider picture it holds; any other address fails to fetch.
type fakeRemoteImages struct{ keys map[string]string }

func (f *fakeRemoteImages) Ensure(_ context.Context, address string) (string, error) {
	if key, ok := f.keys[address]; ok {
		return key, nil
	}
	return "", errors.New("not reachable")
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

// The row's own RootFolderIDs must actually be visible to the caller, or
// this exercises the visibility check's own 404 (below) rather than the
// not-found path this test means to cover -- a real root folder and grant,
// not the bare admin-with-nothing-configured shape the visibility check
// would also 404 on for an unrelated reason.
func TestGetWorkArtwork_NoCoverRecordedAndNoArtistAnswers404(t *testing.T) {
	fixture := newWorksFixture(t)
	_, root := fixture.createCategoryWithRoot(t, "Music", "/media/music")
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")
	fixture.artwork.row = apiserver.WorkArtworkRow{Found: false, RootFolderIDs: []int64{root.ID}}

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/works/1/artwork"))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when no cover has been recorded and the work has no artist either", recorder.Code)
	}
}

// The album-falls-back-to-artist-image chain, 2026-09-11: a work with no
// cover of its own, but a primary artist with a recorded image, serves that
// image instead of 404ing.
func TestGetWorkArtwork_FallsBackToThePrimaryArtistsImage(t *testing.T) {
	fixture := newWorksFixture(t)
	_, root := fixture.createCategoryWithRoot(t, "Music", "/media/music")
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	artistID := int64(42)
	fixture.artwork.row = apiserver.WorkArtworkRow{
		Found: false, RootFolderIDs: []int64{root.ID}, LeadPersonID: &artistID,
	}
	fixture.artwork.personRow = apiserver.PersonArtworkRow{CacheKey: "person-42-cover", Found: true}
	fixture.writeFixtureImage(t, "person-42-cover", "small", []byte("the artist's own photo"))

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/works/1/artwork"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}
	if recorder.Body.String() != "the artist's own photo" {
		t.Errorf("body = %q, want the primary artist's own cached image", recorder.Body.String())
	}
	if fixture.artwork.lastPersonID != 42 {
		t.Errorf("GetPersonArtwork called with id %d, want 42", fixture.artwork.lastPersonID)
	}
}

// A primary artist with no image of their own (the common case) still 404s
// -- the fallback has nothing to fall back to.
func TestGetWorkArtwork_PrimaryArtistWithNoImageStillAnswers404(t *testing.T) {
	fixture := newWorksFixture(t)
	_, root := fixture.createCategoryWithRoot(t, "Music", "/media/music")
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	artistID := int64(42)
	fixture.artwork.row = apiserver.WorkArtworkRow{
		Found: false, RootFolderIDs: []int64{root.ID}, LeadPersonID: &artistID,
	}
	fixture.artwork.personRow = apiserver.PersonArtworkRow{Found: false}

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/works/1/artwork"))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when the primary artist has no image either", recorder.Code)
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

// A wanted album with no files has no root folder; it is visible through the
// category wanting it, as GetWork already treats it, and its cover must be too.
func TestGetWorkArtwork_AWantedWorkIsVisibleThroughItsCategory(t *testing.T) {
	fixture := newWorksFixture(t)
	visible, _ := fixture.createCategoryWithRoot(t, "Music", "/media/music")
	user := fixture.createUser(t, "listener", settings.RoleReadonly)
	if err := fixture.cache.SetCategoryAccess(t.Context(), user.ID, visible.ID, settings.PermissionRead); err != nil {
		t.Fatalf("SetCategoryAccess: %v", err)
	}
	cookies := fixture.signIn(t, "listener")

	fixture.artwork.row = apiserver.WorkArtworkRow{
		CacheKey: "work-1-cover", Found: true, CategoryIDs: []int64{visible.ID},
	}
	fixture.writeFixtureImage(t, "work-1-cover", "small", []byte("wanted cover"))

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/works/1/artwork"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want 200 for a wanted work in a readable category", recorder.Code, recorder.Body)
	}
}

func TestGetWorkArtwork_AWantedWorkInAHiddenCategoryAnswers404(t *testing.T) {
	fixture := newWorksFixture(t)
	hidden, _ := fixture.createCategoryWithRoot(t, "Private Stash", "/media/private")
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	fixture.artwork.row = apiserver.WorkArtworkRow{
		CacheKey: "work-1-cover", Found: true, CategoryIDs: []int64{hidden.ID},
	}
	fixture.writeFixtureImage(t, "work-1-cover", "small", []byte("secret cover"))

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/works/1/artwork"))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a wanted work in a category this caller cannot read", recorder.Code)
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

// GetPersonArtwork has no visibility check of its own (a person is shared,
// GetPerson itself has none either) -- any signed-in caller reaches it.
func TestGetPersonArtwork_ServesTheCachedImage(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	fixture.artwork.personRow = apiserver.PersonArtworkRow{CacheKey: "person-7-cover", Found: true}
	fixture.writeFixtureImage(t, "person-7-cover", "small", []byte("vutu doci 1981"))

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/people/7/artwork"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}
	if recorder.Body.String() != "vutu doci 1981" {
		t.Errorf("body = %q, want the cached image bytes", recorder.Body.String())
	}
	if fixture.artwork.lastPersonID != 7 {
		t.Errorf("GetPersonArtwork called with id %d, want 7", fixture.artwork.lastPersonID)
	}
}

func TestGetPersonArtwork_NoImageAnswers404(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")
	fixture.artwork.personRow = apiserver.PersonArtworkRow{Found: false}

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/people/7/artwork"))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when no image has been recorded", recorder.Code)
	}
}

// R2.0 finding 1: a TV series has no cover of its own, only its provider poster, and the one picture route serves it.
func TestGetWorkArtwork_ServesTheProviderPosterBeforeTheLeadsImage(t *testing.T) {
	fixture := newWorksFixture(t)
	_, root := fixture.createCategoryWithRoot(t, "Series", "/media/tv")
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	leadID := int64(42)
	fixture.artwork.row = apiserver.WorkArtworkRow{
		RootFolderIDs: []int64{root.ID}, PosterURL: "https://image.tmdb.org/t/p/original/poster.jpg", LeadPersonID: &leadID,
	}
	fixture.artwork.personRow = apiserver.PersonArtworkRow{CacheKey: "person-42", Found: true}
	fixture.remoteImages.keys["https://image.tmdb.org/t/p/original/poster.jpg"] = "remote-poster"
	fixture.writeFixtureImage(t, "remote-poster", "small", []byte("the series poster"))
	fixture.writeFixtureImage(t, "person-42", "small", []byte("the lead's photo"))

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/works/1/artwork"))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "the series poster" {
		t.Errorf("status %d, body %q; want 200 and the series poster", recorder.Code, recorder.Body)
	}

	delete(fixture.remoteImages.keys, "https://image.tmdb.org/t/p/original/poster.jpg")
	recorder = fixture.do(authedGet(t, cookies, "/api/v1/works/1/artwork"))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "the lead's photo" {
		t.Errorf("unreachable poster: status %d, body %q; want 200 and the lead's photo", recorder.Code, recorder.Body)
	}
}
