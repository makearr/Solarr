package apiserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
)

// fakeQueue answers canned rows and records removals.
type fakeQueue struct {
	rows      []apiserver.QueueRow
	clients   []apiserver.QueueClientRow
	removeErr error
	removed   []string
}

func (f *fakeQueue) List(context.Context) ([]apiserver.QueueRow, []apiserver.QueueClientRow, error) {
	return f.rows, f.clients, nil
}

func (f *fakeQueue) Remove(_ context.Context, id int64, blocklist bool) error {
	if f.removeErr != nil {
		return f.removeErr
	}
	entry := "download " + string(rune('0'+id))
	if blocklist {
		entry += " blocklisted"
	}
	f.removed = append(f.removed, entry)
	return nil
}

type queueFixture struct {
	worksFixture
	visible, hidden settings.Category
}

func newQueueFixture(t *testing.T) queueFixture {
	t.Helper()
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
	fixture.queue.rows = []apiserver.QueueRow{
		{ID: 1, WorkID: &workID, CategoryID: visible.ID, Album: "Zanuvode", Artist: "Heka", ReleaseTitle: "Heka-Zanuvode",
			Client: "Home SABnzbd", State: "DOWNLOADING", ClientState: "downloading", SizeBytes: 1000, SizeLeftBytes: 400,
			AddedAt: time.Unix(1, 0).UTC()},
		{ID: 2, CategoryID: hidden.ID, ReleaseTitle: "Hidden-Album", Client: "Home SABnzbd", State: "QUEUED",
			AddedAt: time.Unix(2, 0).UTC()},
	}
	fixture.queue.clients = []apiserver.QueueClientRow{{ID: 1, Name: "Home SABnzbd", Paused: true, Items: 2}}
	return queueFixture{worksFixture: fixture, visible: visible, hidden: hidden}
}

// Only downloads for the caller's libraries; the client with its paused state for everyone.
func TestQueue_ScopedToTheCallersLibraries(t *testing.T) {
	fixture := newQueueFixture(t)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/queue"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, %s", recorder.Code, recorder.Body)
	}
	var body struct {
		Clients []map[string]any `json:"clients"`
		Items   []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	wantItems := []map[string]any{{
		"id": 1.0, "work_id": 7.0, "album": "Zanuvode", "artist": "Heka", "release_title": "Heka-Zanuvode",
		"client": "Home SABnzbd", "state": "DOWNLOADING", "client_state": "downloading",
		"size_bytes": 1000.0, "size_left_bytes": 400.0, "added_at": "1970-01-01T00:00:01Z",
	}}
	if !reflect.DeepEqual(body.Items, wantItems) {
		t.Errorf("items = %v\nwant %v", body.Items, wantItems)
	}
	wantClients := []map[string]any{{
		"id": 1.0, "name": "Home SABnzbd", "paused": true, "speed_bytes_per_sec": 0.0, "items": 2.0, "size_left_bytes": 0.0,
	}}
	if !reflect.DeepEqual(body.Clients, wantClients) {
		t.Errorf("clients = %v\nwant %v", body.Clients, wantClients)
	}
}

// One album's downloads, for its page.
func TestQueue_FiltersToOneAlbum(t *testing.T) {
	fixture := newQueueFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/queue?work_id=8"))
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 0 {
		t.Errorf("items = %v, want none for another album", body.Items)
	}
}

// Removing needs manage on the library; a hidden download does not exist for the caller.
func TestRemoveQueueItem_NeedsManageAndHidesOtherLibraries(t *testing.T) {
	fixture := newQueueFixture(t)
	cookies := fixture.signIn(t, "listener")

	if recorder := fixture.do(authedDelete(t, cookies, "/api/v1/queue/1")); recorder.Code != http.StatusForbidden {
		t.Errorf("read-only remove: status = %d, want 403", recorder.Code)
	}
	if recorder := fixture.do(authedDelete(t, cookies, "/api/v1/queue/2")); recorder.Code != http.StatusNotFound {
		t.Errorf("hidden remove: status = %d, want 404", recorder.Code)
	}
	if len(fixture.queue.removed) != 0 {
		t.Errorf("removed %v, want nothing", fixture.queue.removed)
	}
}

// An administrator removes and blocklists; a download being imported is a conflict.
func TestRemoveQueueItem_RemovesAndReportsAConflict(t *testing.T) {
	fixture := newQueueFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	if recorder := fixture.do(authedDelete(t, cookies, "/api/v1/queue/1?blocklist=true")); recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, %s", recorder.Code, recorder.Body)
	}
	if !reflect.DeepEqual(fixture.queue.removed, []string{"download 1 blocklisted"}) {
		t.Errorf("removed = %v", fixture.queue.removed)
	}

	fixture.queue.removeErr = apiserver.ErrQueueItemNotRemovable
	if recorder := fixture.do(authedDelete(t, cookies, "/api/v1/queue/1")); recorder.Code != http.StatusConflict {
		t.Errorf("importing remove: status = %d, want 409", recorder.Code)
	}
}
