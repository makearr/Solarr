package apiserver_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
)

type fakeDuplicates struct{}

func (fakeDuplicates) Duplicates(_ context.Context, roots []int64, _ int) ([]apiserver.DuplicateGroupRow, error) {
	if len(roots) == 0 {
		return nil, nil
	}
	return []apiserver.DuplicateGroupRow{{SizeBytes: 100, Copies: []apiserver.DuplicateCopyRow{
		{MediaObjectID: 1, Paths: []apiserver.DuplicatePathRow{{RootFolderID: roots[0], RelPath: "A/01.flac"}}},
		{MediaObjectID: 2, Paths: []apiserver.DuplicatePathRow{{RootFolderID: roots[0], RelPath: "B/01.flac"}}},
	}}}, nil
}

func TestCategoryDuplicates_AnswersGroupsWithFullPathsAndWhatKeepingOneFrees(t *testing.T) {
	fixture := newWorksFixture(t)
	category, _ := fixture.createCategoryWithRoot(t, "Music", "/media/music")
	user := fixture.createUser(t, "listener", settings.RoleReadonly)
	if err := fixture.cache.SetCategoryAccess(t.Context(), user.ID, category.ID, settings.PermissionRead); err != nil {
		t.Fatalf("SetCategoryAccess: %v", err)
	}
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/categories/1/duplicates"))
	want := `{"reclaimable_bytes":100,"groups":[{"size_bytes":100,"reclaimable_bytes":100,"copies":[` +
		`{"media_object_id":1,"paths":["/media/music/A/01.flac"]},{"media_object_id":2,"paths":["/media/music/B/01.flac"]}]}]}` + "\n"
	if recorder.Code != http.StatusOK || recorder.Body.String() != want {
		t.Fatalf("status %d, body %q, want %q", recorder.Code, recorder.Body, want)
	}
}

func TestCategoryDuplicates_ALibraryTheCallerCannotReadIs404(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createCategoryWithRoot(t, "Private", "/media/private")
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")
	if recorder := fixture.do(authedGet(t, cookies, "/api/v1/categories/1/duplicates")); recorder.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", recorder.Code)
	}
}
