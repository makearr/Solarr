package apiserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/secrets"
)

type fakeWatchState struct {
	watches map[int64]apiserver.WorkWatchRow
	pairs   []apiserver.WatchPair
	watched []apiserver.WatchedWorkRow
	files   map[int64][]int64
	lastSel apiserver.WatchedSelection
}

func (f *fakeWatchState) WatchOf(_ context.Context, pairs []apiserver.WatchPair, _ []int64) (map[int64]apiserver.WorkWatchRow, error) {
	f.pairs = pairs
	return f.watches, nil
}
func (f *fakeWatchState) PartsOf(context.Context, []apiserver.WatchPair, int64) ([]apiserver.PartPlayRow, error) {
	return nil, nil
}
func (f *fakeWatchState) WatchedWorks(_ context.Context, selection apiserver.WatchedSelection) ([]apiserver.WatchedWorkRow, error) {
	f.lastSel = selection
	return f.watched, nil
}
func (f *fakeWatchState) WorkFileIDs(_ context.Context, id int64) ([]int64, error) {
	return f.files[id], nil
}

type watchListBody struct {
	Items []struct {
		ID    int64 `json:"id"`
		Watch *struct {
			Status    string `json:"status"`
			WatchedBy *int   `json:"watched_by"`
			Of        *int   `json:"of"`
		} `json:"watch"`
	} `json:"items"`
}

func watchFixture(t *testing.T) (worksFixture, []*http.Cookie, settings.User) {
	t.Helper()
	fixture, adminCookies := mediaServerFixture(t)
	viewer := fixture.createUser(t, "viewer", settings.RoleUser)
	category, _ := fixture.createCategoryWithRoot(t, "Shows", "/tv")
	server, err := fixture.cache.CreateMediaServer(t.Context(), settings.MediaServerRequest{
		Kind: "jellyfin", Name: "Den", BaseURL: "http://den.local", Enabled: true, MediaServerUse: settings.MediaServerUse{SyncPlayback: true},
	}, secrets.Secret("key"), 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.cache.SetMediaServerLibraries(t.Context(), server.ID, []settings.MediaServerLibrary{{CategoryID: category.ID, ServerLibraryID: "l1"}}); err != nil {
		t.Fatal(err)
	}
	mapped := viewer.ID
	if err := fixture.cache.SetMediaServerUsers(t.Context(), server.ID, []settings.MediaServerUser{
		{ServerUserID: "u1", Name: "Ada", UserID: &mapped, Counted: true}, {ServerUserID: "u2", Name: "Bo", Counted: true},
	}); err != nil {
		t.Fatal(err)
	}
	fixture.works.rows = []apiserver.WorkRow{{ID: 5, MediaType: "tv", Title: "Show", SortTitle: "show"}}
	fixture.watchState.watches = map[int64]apiserver.WorkWatchRow{5: {Finished: 1, Started: 1, LastPlayedAt: 99}}
	return fixture, adminCookies, viewer
}

func listWatch(t *testing.T, fixture worksFixture, cookies []*http.Cookie) watchListBody {
	t.Helper()
	recorder := fixture.do(authedGet(t, cookies, "/api/v1/works?watched=yes"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}
	var body watchListBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestListWorks_ShowsAMappedUserTheirOwnWatchStateAndAnAdministratorTheCount(t *testing.T) {
	fixture, adminCookies, _ := watchFixture(t)

	own := listWatch(t, fixture, fixture.signIn(t, "viewer"))
	if own.Items[0].Watch == nil || own.Items[0].Watch.Status != "yes" || own.Items[0].Watch.WatchedBy != nil {
		t.Fatalf("a mapped user sees their own state, without counts: %+v", own.Items[0].Watch)
	}
	if want := []apiserver.WatchPair{{ServerID: fixture.watchState.pairs[0].ServerID, ServerUserID: "u1"}}; !reflect.DeepEqual(fixture.watchState.pairs, want) {
		t.Fatalf("pairs %+v", fixture.watchState.pairs)
	}
	if fixture.works.lastFilter.Watched != "yes" {
		t.Fatalf("filter %q", fixture.works.lastFilter.Watched)
	}

	everyone := listWatch(t, fixture, adminCookies)
	if watch := everyone.Items[0].Watch; watch == nil || watch.WatchedBy == nil || *watch.WatchedBy != 1 || *watch.Of != 2 {
		t.Fatalf("an administrator sees watched by 1 of 2: %+v", watch)
	}
}

func TestListWorks_ShowsAnUnmappedNonAdministratorNoWatchStateAndDoesNotFilter(t *testing.T) {
	fixture, _, _ := watchFixture(t)
	fixture.createUser(t, "guest", settings.RoleUser)

	body := listWatch(t, fixture, fixture.signIn(t, "guest"))
	if body.Items[0].Watch != nil || fixture.works.lastFilter.Watched != "" {
		t.Fatalf("watch %+v filter %q", body.Items[0].Watch, fixture.works.lastFilter.Watched)
	}
}

func TestRecycleWatchedWorks_RecyclesEveryFileOfTheListedWorksOnlyOutsideAPreview(t *testing.T) {
	fixture, adminCookies, _ := watchFixture(t)
	fixture.watchState.watched = []apiserver.WatchedWorkRow{{ID: 5, Title: "Show", SizeBytes: 300}}
	fixture.watchState.files = map[int64][]int64{5: {11, 12}}
	category := fixture.cache.Snapshot().Categories()[0]
	target := "/api/v1/libraries/" + itoa(category.ID) + "/watched/recycle"

	preview := fixture.do(authedPost(t, adminCookies, target, `{"work_ids":[5,6],"preview":true}`))
	if preview.Code != http.StatusOK || fixture.removeAndBlocker.calls != 0 || !contains(preview.Body.String(), `"files":2`) {
		t.Fatalf("preview %d %s, recycled %d", preview.Code, preview.Body, fixture.removeAndBlocker.calls)
	}
	if !reflect.DeepEqual(fixture.watchState.lastSel.IDs, []int64{5, 6}) {
		t.Fatalf("selection %+v", fixture.watchState.lastSel)
	}

	done := fixture.do(authedPost(t, adminCookies, target, `{"work_ids":[5,6]}`))
	if done.Code != http.StatusOK || fixture.removeAndBlocker.calls != 2 || !fixture.removeAndBlocker.lastReq.RecycleBin {
		t.Fatalf("done %d %s, recycled %d", done.Code, done.Body, fixture.removeAndBlocker.calls)
	}

	refused := fixture.do(authedPost(t, fixture.signIn(t, "viewer"), target, `{"work_ids":[5]}`))
	if refused.Code != http.StatusForbidden {
		t.Fatalf("a non-administrator got %d", refused.Code)
	}
}
