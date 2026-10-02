package apiserver_test

import (
	"context"
	"database/sql"
	"net/http"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
)

type fakeWorkEditor struct {
	row   apiserver.WorkEditRow
	err   error
	saved *apiserver.WorkEditRow

	bulkSelection apiserver.BulkSelection
	bulkChange    apiserver.BulkEditChange
	bulkPreview   bool
}

func (f *fakeWorkEditor) GetWorkEdit(context.Context, int64) (apiserver.WorkEditRow, error) {
	return f.row, f.err
}

func (f *fakeWorkEditor) SaveWorkEdit(_ context.Context, _ int64, edit apiserver.WorkEditRow) error {
	f.saved = &edit
	return f.err
}

func (f *fakeWorkEditor) BulkEdit(
	_ context.Context, selection apiserver.BulkSelection, change apiserver.BulkEditChange, preview bool,
) (int64, error) {
	f.bulkSelection, f.bulkChange, f.bulkPreview = selection, change, preview
	return 7, f.err
}

func (f *fakeWorkEditor) ListTags(context.Context) ([]string, error) {
	return []string{"Rock", "Sleep"}, nil
}

func TestWorkEdit_ReadsAndSavesTheWholeDialog(t *testing.T) {
	fixture, cookies := visibleWorksFixture(t)
	profile := int64(4)
	fixture.workEditor.row = apiserver.WorkEditRow{Monitored: true, ProfileID: &profile, Tags: []string{"Sleep"}}

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/works/1/edit"))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "{\"monitored\":true,\"profile_id\":4,\"tags\":[\"Sleep\"]}\n" {
		t.Fatalf("get: status %d, body %q", recorder.Code, recorder.Body)
	}

	recorder = fixture.do(authedPut(t, cookies, "/api/v1/works/1/edit", `{"monitored":false,"profile_id":null,"tags":["Rock"]}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("put: status %d, body %s", recorder.Code, recorder.Body)
	}
	if want := (apiserver.WorkEditRow{Tags: []string{"Rock"}}); !reflect.DeepEqual(*fixture.workEditor.saved, want) {
		t.Fatalf("saved %+v, want %+v", *fixture.workEditor.saved, want)
	}
}

func TestWorkEdit_AnUnknownProfileIs400AndAMissingWorkIs404(t *testing.T) {
	fixture, cookies := visibleWorksFixture(t)
	fixture.workEditor.err = apiserver.ErrInvalidWorkEdit
	recorder := fixture.do(authedPut(t, cookies, "/api/v1/works/1/edit", `{"monitored":true,"profile_id":99,"tags":[]}`))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("unknown profile: status %d, want 400", recorder.Code)
	}
	fixture.workEditor.err = sql.ErrNoRows
	recorder = fixture.do(authedGet(t, cookies, "/api/v1/works/1/edit"))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("missing work: status %d, want 404", recorder.Code)
	}
}

func TestListTags_AnswersNames(t *testing.T) {
	fixture, cookies := visibleWorksFixture(t)
	recorder := fixture.do(authedGet(t, cookies, "/api/v1/tags"))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "[\"Rock\",\"Sleep\"]\n" {
		t.Fatalf("status %d, body %q", recorder.Code, recorder.Body)
	}
}

func TestBulkEditWorks_ForwardsTheSelectionAndOnlyWhatIsPresent(t *testing.T) {
	fixture, cookies := visibleWorksFixture(t)
	recorder := fixture.do(authedPost(t, cookies, "/api/v1/works/bulk/edit",
		`{"category_id":1,"excluded_ids":[3],"fresh":true,"profile":{"id":null},"add_tags":["Road trip"],"preview":true}`))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "{\"count\":7}\n" {
		t.Fatalf("status %d, body %q", recorder.Code, recorder.Body)
	}
	editor := fixture.workEditor
	if !editor.bulkSelection.Fresh || !reflect.DeepEqual(editor.bulkSelection.ExcludedIDs, []int64{3}) || !editor.bulkPreview {
		t.Errorf("selection %+v preview %v", editor.bulkSelection, editor.bulkPreview)
	}
	want := apiserver.BulkEditChange{SetProfile: true, AddTags: []string{"Road trip"}}
	if !reflect.DeepEqual(editor.bulkChange, want) {
		t.Errorf("change %+v, want %+v", editor.bulkChange, want)
	}
}

func TestBulkEditWorks_NeedsRequestPermissionOnTheLibrary(t *testing.T) {
	fixture := newWorksFixture(t)
	category, _ := fixture.createCategoryWithRoot(t, "Music", "/media/music")
	user := fixture.createUser(t, "reader", settings.RoleReadonly)
	if err := fixture.cache.SetCategoryAccess(t.Context(), user.ID, category.ID, settings.PermissionRead); err != nil {
		t.Fatalf("SetCategoryAccess: %v", err)
	}
	cookies := fixture.signIn(t, "reader")
	recorder := fixture.do(authedPost(t, cookies, "/api/v1/works/bulk/edit", `{"category_id":1,"monitored":true}`))
	if recorder.Code == http.StatusOK {
		t.Fatalf("a read-only user bulk-edited: status %d", recorder.Code)
	}
}
