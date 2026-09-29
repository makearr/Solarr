package apiserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
)

// fakeHistory answers canned rows and records what it was asked for.
type fakeHistory struct {
	rows             []apiserver.HistoryRow
	workID, beforeID int64
	limit            int
}

func (f *fakeHistory) List(_ context.Context, workID, beforeID int64, limit int) ([]apiserver.HistoryRow, error) {
	f.workID, f.beforeID, f.limit = workID, beforeID, limit
	return f.rows, nil
}

// History shows only the caller's libraries, and pages on what was read, not what was shown.
func TestHistory_ScopedToTheCallersLibrariesAndPagedOnWhatWasRead(t *testing.T) {
	fixture := newWorksFixture(t)
	visible, err := fixture.cache.CreateCategory(t.Context(), "music", "Music", 10)
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := fixture.cache.CreateCategory(t.Context(), "music", "Hidden", 20)
	if err != nil {
		t.Fatal(err)
	}
	user := fixture.createUser(t, "listener", settings.RoleReadonly)
	if err := fixture.cache.SetCategoryAccess(t.Context(), user.ID, visible.ID, settings.PermissionRead); err != nil {
		t.Fatal(err)
	}
	workID := int64(7)
	fixture.history.rows = []apiserver.HistoryRow{
		{ID: 9, Kind: "imported", WorkID: &workID, CategoryID: visible.ID, ReleaseTitle: "Heka-Zanuvode", Album: "Zanuvode"},
		{ID: 8, Kind: "grabbed", CategoryID: hidden.ID, ReleaseTitle: "Hidden-Album"},
		{ID: 5, Kind: "failed", ReleaseTitle: "No-Library", Detail: "Repair failed"},
	}
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/history?work_id=7&after=10&limit=3"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, %s", recorder.Code, recorder.Body)
	}
	var page struct {
		Items []struct {
			ID           int64  `json:"id"`
			Kind         string `json:"kind"`
			ReleaseTitle string `json:"release_title"`
			Album        string `json:"album"`
		} `json:"items"`
		NextCursor *int64 `json:"next_cursor"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != 9 || page.Items[0].Kind != "imported" || page.Items[0].Album != "Zanuvode" {
		t.Errorf("items = %+v, want only entry 9 from the visible library", page.Items)
	}
	if page.NextCursor == nil || *page.NextCursor != 5 {
		t.Errorf("next cursor = %v, want 5: the last entry read", page.NextCursor)
	}
	if got, want := []int64{fixture.history.workID, fixture.history.beforeID, int64(fixture.history.limit)}, []int64{7, 10, 3}; !reflect.DeepEqual(got, want) {
		t.Errorf("asked for work, before, limit = %v, want %v", got, want)
	}
}
