package apiserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
)

type downloadFolderBody struct {
	ID             int64  `json:"id"`
	Path           string `json:"path"`
	Label          string `json:"label"`
	CategoryID     *int64 `json:"category_id"`
	Readable       bool   `json:"readable"`
	ReadableReason string `json:"readable_reason"`
}

func TestAddDownloadFolder_ProbesReadabilityAndPersists(t *testing.T) {
	fixture := newWorksFixture(t)
	if err := fixture.fsys.MkdirAll("/downloads/sabnzbd", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/downloadfolders",
		`{"path":"/downloads/sabnzbd","label":"sabnzbd"}`))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var created downloadFolderBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if created.Path != "/downloads/sabnzbd" || !created.Readable {
		t.Errorf("created = %+v, want a readable folder", created)
	}

	listRecorder := fixture.do(authedGet(t, cookies, "/api/v1/downloadfolders"))
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", listRecorder.Code, listRecorder.Body)
	}
	var listed []downloadFolderBody
	if err := json.Unmarshal(listRecorder.Body.Bytes(), &listed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != created.ID {
		t.Errorf("listed = %+v, want exactly the one created", listed)
	}
}

// A path that does not exist is a typo, refused outright -- the same
// Validate check AddRootFolder's own precedent makes. Readable's own
// "flagged, not refused" stance (an NFS-mounted client folder owned by
// another uid, readable=false but still accepted since there is nothing to
// chown) applies only once Validate passes; it needs a real permission
// failure to exercise, which internal/platform/fsx.Mem has no fault injection for.
func TestAddDownloadFolder_RefusesAnUnusablePath(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/downloadfolders",
		`{"path":"/downloads/does-not-exist"}`))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", recorder.Code, recorder.Body)
	}
}

func TestAddDownloadFolder_RequiresAdministrator(t *testing.T) {
	fixture := newWorksFixture(t)

	recorder := fixture.do(authedPost(t, nil, "/api/v1/downloadfolders", `{"path":"/downloads/sabnzbd"}`))
	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for an unauthenticated add", recorder.Code)
	}
}

func TestAddDownloadFolder_RejectsAnUnknownCategory(t *testing.T) {
	fixture := newWorksFixture(t)
	if err := fixture.fsys.MkdirAll("/downloads/sabnzbd", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/downloadfolders",
		`{"path":"/downloads/sabnzbd","category_id":999}`))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for an unknown category (%s)", recorder.Code, recorder.Body)
	}
}

func TestDeleteDownloadFolder_RemovesIt(t *testing.T) {
	fixture := newWorksFixture(t)
	if err := fixture.fsys.MkdirAll("/downloads/sabnzbd", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	created := fixture.do(authedPost(t, cookies, "/api/v1/downloadfolders", `{"path":"/downloads/sabnzbd"}`))
	var body downloadFolderBody
	if err := json.Unmarshal(created.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	deleted := fixture.do(authedDelete(t, cookies, "/api/v1/downloadfolders/"+itoa(body.ID)))
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204 (%s)", deleted.Code, deleted.Body)
	}

	listRecorder := fixture.do(authedGet(t, cookies, "/api/v1/downloadfolders"))
	var listed []downloadFolderBody
	if err := json.Unmarshal(listRecorder.Body.Bytes(), &listed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(listed) != 0 {
		t.Errorf("listed = %+v, want none after delete", listed)
	}
}

func TestDeleteDownloadFolder_RequiresAdministrator(t *testing.T) {
	fixture := newWorksFixture(t)

	recorder := fixture.do(authedDelete(t, nil, "/api/v1/downloadfolders/1"))
	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for an unauthenticated delete", recorder.Code)
	}
}

func TestListDownloadFolderContents_ReadsLiveTopLevelEntries(t *testing.T) {
	fixture := newWorksFixture(t)
	if err := fixture.fsys.MkdirAll("/downloads/sabnzbd/unfinished", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	file, err := fixture.fsys.Create("/downloads/sabnzbd/movie.nzb")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	created := fixture.do(authedPost(t, cookies, "/api/v1/downloadfolders", `{"path":"/downloads/sabnzbd"}`))
	var body downloadFolderBody
	if err := json.Unmarshal(created.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/downloadfolders/"+itoa(body.ID)+"/contents"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", recorder.Code, recorder.Body)
	}
	var entries []struct {
		Name  string `json:"name"`
		IsDir bool   `json:"is_dir"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &entries); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %+v, want 2 (one file, one directory)", entries)
	}
	foundFile, foundDir := false, false
	for _, entry := range entries {
		if entry.Name == "movie.nzb" && !entry.IsDir {
			foundFile = true
		}
		if entry.Name == "unfinished" && entry.IsDir {
			foundDir = true
		}
	}
	if !foundFile || !foundDir {
		t.Errorf("entries = %+v, want movie.nzb (file) and unfinished (dir)", entries)
	}
}

func TestListDownloadFolderContents_UnknownIDAnswers404(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/downloadfolders/999/contents"))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for an unknown download folder", recorder.Code)
	}
}
