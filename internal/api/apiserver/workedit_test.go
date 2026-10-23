package apiserver_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"reflect"
	"strings"
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
	edited        string
	merged        string
	deleted       int64
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

func (f *fakeWorkEditor) TagTree(_ context.Context, taxonomy string) ([]apiserver.TagRow, error) {
	return []apiserver.TagRow{
		{ID: 1, Name: "Rock", Taxonomy: "music", Source: "taxonomy", Works: 4, Aliases: []string{"rock-music"}},
		{ID: 2, Name: "Metal", ParentID: 1, Taxonomy: "music", Source: "musicbrainz", Works: 2},
		{ID: 3, Name: taxonomy, Source: "user"},
	}, nil
}

func (f *fakeWorkEditor) CreateTag(_ context.Context, name, taxonomy string, parentID int64) (apiserver.TagRow, error) {
	return apiserver.TagRow{ID: 9, Name: name, ParentID: parentID, Taxonomy: taxonomy, Source: "user"}, f.err
}

func (f *fakeWorkEditor) EditTag(_ context.Context, id int64, name string, parentID int64) error {
	f.edited = fmt.Sprintf("%d=%s under %d", id, name, parentID)
	return f.err
}

func (f *fakeWorkEditor) MergeTag(_ context.Context, fromID, intoID int64) error {
	f.merged = fmt.Sprintf("%d into %d", fromID, intoID)
	return f.err
}

func (f *fakeWorkEditor) DeleteTag(_ context.Context, id int64) error {
	f.deleted = id
	return f.err
}

func TestWorkEdit_ReadsAndSavesTheWholeDialog(t *testing.T) {
	fixture, cookies := visibleWorksFixture(t)
	profile := int64(4)
	fixture.workEditor.row = apiserver.WorkEditRow{Monitored: true, ProfileID: &profile, Tags: []string{"Sleep"}, RootFolderIDs: []int64{2}}

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/works/1/edit"))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "{\"monitored\":true,\"profile_id\":4,\"root_folder_ids\":[2],\"tags\":[\"Sleep\"]}\n" {
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

// The tree comes with parents, aliases and counts; taxonomy narrows it, "*" when absent.
func TestListTags_AnswersTheTree(t *testing.T) {
	fixture, cookies := visibleWorksFixture(t)
	recorder := fixture.do(authedGet(t, cookies, "/api/v1/tags?taxonomy="))
	want := `[{"aliases":["rock-music"],"id":1,"name":"Rock","source":"taxonomy","taxonomy":"music","works":4},` +
		`{"aliases":[],"id":2,"name":"Metal","parent_id":1,"source":"musicbrainz","taxonomy":"music","works":2},` +
		`{"aliases":[],"id":3,"name":"","source":"user","taxonomy":"","works":0}]` + "\n"
	if recorder.Code != http.StatusOK || recorder.Body.String() != want {
		t.Fatalf("status %d, body %q", recorder.Code, recorder.Body)
	}
	if recorder := fixture.do(authedGet(t, cookies, "/api/v1/tags")); !strings.Contains(recorder.Body.String(), `"name":"*"`) {
		t.Fatalf("without taxonomy: body %q, want every taxonomy asked for", recorder.Body)
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

func TestTags_ChangingThemIsAdministratorWork(t *testing.T) {
	fixture, cookies := visibleWorksFixture(t) // a read-only listener
	for _, request := range []*http.Request{
		authedPut(t, cookies, "/api/v1/tags/3", `{"name":"Nap","parent_id":null}`),
		authedDelete(t, cookies, "/api/v1/tags/3"),
		authedPost(t, cookies, "/api/v1/tags/3/merge", `{"into_id":1}`),
		authedPost(t, cookies, "/api/v1/tags", `{"name":"Nap","taxonomy":""}`),
	} {
		if recorder := fixture.do(request); recorder.Code != http.StatusForbidden {
			t.Fatalf("%s %s by a listener: status %d, want 403", request.Method, request.URL.Path, recorder.Code)
		}
	}
	if fixture.workEditor.edited != "" || fixture.workEditor.merged != "" || fixture.workEditor.deleted != 0 {
		t.Fatal("a listener reached the tag store")
	}
}

func TestTags_AnAdministratorEditsMergesDeletesAndConflictsAre409(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	if recorder := fixture.do(authedPut(t, cookies, "/api/v1/tags/3", `{"name":"  Nap ","parent_id":1}`)); recorder.Code != http.StatusNoContent {
		t.Fatalf("edit: status %d, body %s", recorder.Code, recorder.Body)
	}
	if fixture.workEditor.edited != "3=Nap under 1" {
		t.Errorf("edited %q, want the trimmed name under 1", fixture.workEditor.edited)
	}
	if recorder := fixture.do(authedPost(t, cookies, "/api/v1/tags/3/merge", `{"into_id":1}`)); recorder.Code != http.StatusNoContent || fixture.workEditor.merged != "3 into 1" {
		t.Fatalf("merge: status %d, merged %q", recorder.Code, fixture.workEditor.merged)
	}
	if recorder := fixture.do(authedPost(t, cookies, "/api/v1/tags", `{"name":"Jazz","taxonomy":"music"}`)); recorder.Code != http.StatusOK ||
		recorder.Body.String() != `{"aliases":[],"id":9,"name":"Jazz","source":"user","taxonomy":"music","works":0}`+"\n" {
		t.Fatalf("create: status %d, body %q", recorder.Code, recorder.Body)
	}
	if recorder := fixture.do(authedDelete(t, cookies, "/api/v1/tags/3")); recorder.Code != http.StatusNoContent || fixture.workEditor.deleted != 3 {
		t.Fatalf("delete: status %d, deleted %d", recorder.Code, fixture.workEditor.deleted)
	}
	if recorder := fixture.do(authedPut(t, cookies, "/api/v1/tags/3", `{"name":"  ","parent_id":null}`)); recorder.Code != http.StatusBadRequest {
		t.Fatalf("blank name: status %d, want 400", recorder.Code)
	}
	for _, refusal := range []error{apiserver.ErrTagNameTaken, apiserver.ErrTagMove} {
		fixture.workEditor.err = refusal
		if recorder := fixture.do(authedPut(t, cookies, "/api/v1/tags/3", `{"name":"Rock","parent_id":null}`)); recorder.Code != http.StatusConflict {
			t.Fatalf("%v: status %d, want 409", refusal, recorder.Code)
		}
	}
}

// The list's tag chips reach the selection, a negative id as one to leave out; anything else is 400.
func TestBulkEditWorks_CarriesTheTagChips(t *testing.T) {
	fixture, cookies := visibleWorksFixture(t)
	recorder := fixture.do(authedPost(t, cookies, "/api/v1/works/bulk/edit", `{"category_id":1,"tags":"4, -7","preview":true}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", recorder.Code, recorder.Body)
	}
	if selection := fixture.workEditor.bulkSelection; !reflect.DeepEqual(selection.TagIDs, []int64{4}) || !reflect.DeepEqual(selection.ExcludedTagIDs, []int64{7}) {
		t.Errorf("selection tags %v, without %v; want [4] and [7]", selection.TagIDs, selection.ExcludedTagIDs)
	}
	if recorder := fixture.do(authedPost(t, cookies, "/api/v1/works/bulk/edit", `{"category_id":1,"tags":"rock"}`)); recorder.Code != http.StatusBadRequest {
		t.Fatalf("a tag name for an id: status %d, want 400", recorder.Code)
	}
}
