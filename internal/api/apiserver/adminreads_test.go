package apiserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/db/settings"
)

// fakeCrossovers answers one event; every other method is left to the nil interface, since no test here calls it.
type fakeCrossovers struct {
	apiserver.Crossovers
	event gen.Crossover
}

func (f *fakeCrossovers) Get(context.Context, int64) (gen.Crossover, error) {
	if f.event.Id == 0 {
		return gen.Crossover{}, apiserver.ErrNoCrossover
	}
	return f.event, nil
}

// What a library's own pages do not need stays with administrators; the dashboard stays everyone's (user, 2026-10-24).
func TestAdministratorOnlyReads(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "reader", settings.RoleReadonly)
	cookies := fixture.signIn(t, "reader")
	for _, target := range []string{
		"/api/v1/recyclebin/entries", "/api/v1/packs", "/api/v1/packs/1", "/api/v1/library/health", "/api/v1/import/sessions",
		"/api/v1/import/sessions/1", "/api/v1/import/sessions/1/units", "/api/v1/downloadfolders/1/contents",
		"/api/v1/people/1/artwork/options",
	} {
		if recorder := fixture.do(authedGet(t, cookies, target)); recorder.Code != http.StatusForbidden {
			t.Errorf("GET %s = %d, want 403 for a non-administrator", target, recorder.Code)
		}
	}
}

// A library manager sees the roots of the libraries they manage, not those they only read; an administrator sees all.
func TestListRootFolders_ShowsOthersOnlyTheRootsOfLibrariesTheyManage(t *testing.T) {
	fixture := newWorksFixture(t)
	managed, shared := fixture.createCategoryWithRoot(t, "Managed", "/media/managed")
	readable, own := fixture.createCategoryWithRoot(t, "Readable", "/media/readable")
	user := fixture.createUser(t, "manager", settings.RoleReadonly)
	for category, permission := range map[int64]settings.Permission{managed.ID: settings.PermissionManage, readable.ID: settings.PermissionRead} {
		if err := fixture.cache.SetCategoryAccess(t.Context(), user.ID, category, permission); err != nil {
			t.Fatal(err)
		}
	}
	fixture.createUser(t, "admin", settings.RoleAdmin)

	list := func(username string) []gen.RootFolder {
		t.Helper()
		recorder := fixture.do(authedGet(t, fixture.signIn(t, username), "/api/v1/rootfolders"))
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s: status %d (%s)", username, recorder.Code, recorder.Body)
		}
		var roots []gen.RootFolder
		if err := json.Unmarshal(recorder.Body.Bytes(), &roots); err != nil {
			t.Fatal(err)
		}
		return roots
	}
	got := list("manager")
	if len(got) != 1 || got[0].Id != shared.ID || got[0].Categories == nil ||
		!slices.Equal(*got[0].Categories, []gen.RootFolderCategory{{Id: managed.ID, Name: "Managed"}}) {
		t.Errorf("manager sees %+v, want only root %d naming only Managed", got, shared.ID)
	}
	var ids []int64
	for _, root := range list("admin") {
		ids = append(ids, root.Id)
	}
	if slices.Sort(ids); !slices.Equal(ids, []int64{shared.ID, own.ID}) {
		t.Errorf("administrator sees roots %v, want both %v", ids, []int64{shared.ID, own.ID})
	}
}

// A crossover answers only to someone who can see one of its series; to anyone else it does not exist.
func TestGetCrossover_AnswersOnlyWhenOneOfItsSeriesIsVisible(t *testing.T) {
	fixture := newWorksFixture(t)
	visible, _ := fixture.createCategoryWithRoot(t, "Visible", "/media/visible")
	hidden, _ := fixture.createCategoryWithRoot(t, "Hidden", "/media/hidden")
	user := fixture.createUser(t, "viewer", settings.RoleReadonly)
	if err := fixture.cache.SetCategoryAccess(t.Context(), user.ID, visible.ID, settings.PermissionRead); err != nil {
		t.Fatal(err)
	}
	cookies := fixture.signIn(t, "viewer")
	fixture.crossovers.event = gen.Crossover{Id: 4, Name: "Event", Members: []gen.CrossoverMember{{PartId: 1, WorkId: 7}}}

	fixture.workDetail.row = apiserver.WorkDetailRow{ID: 7, CategoryIDs: []int64{hidden.ID}}
	if recorder := fixture.do(authedGet(t, cookies, "/api/v1/crossovers/4")); recorder.Code != http.StatusNotFound {
		t.Errorf("series in a hidden library: status %d, want 404", recorder.Code)
	}
	fixture.workDetail.row = apiserver.WorkDetailRow{ID: 7, CategoryIDs: []int64{visible.ID}}
	if recorder := fixture.do(authedGet(t, cookies, "/api/v1/crossovers/4")); recorder.Code != http.StatusOK {
		t.Errorf("series in a visible library: status %d (%s), want 200", recorder.Code, recorder.Body)
	}
}
