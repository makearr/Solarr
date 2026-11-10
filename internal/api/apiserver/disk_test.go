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

type fakeDiskManager struct {
	overrides map[string]string
	measured  int
	scanHours int64
}

func (f *fakeDiskManager) Report(context.Context) (apiserver.DiskReport, error) {
	return apiserver.DiskReport{
		Paths:   []apiserver.DiskPath{{Path: "/mnt/media", Group: "disk:sda", DetectedGroup: "disk:sda"}},
		Inotify: apiserver.DiskInotify{MaxUserWatches: 1000, BudgetWatches: 500},
	}, nil
}

func (f *fakeDiskManager) SetOverrides(_ context.Context, overrides map[string]string) error {
	f.overrides = overrides
	return nil
}

func (f *fakeDiskManager) SetScanInterval(_ context.Context, hours int64) error {
	f.scanHours = hours
	return nil
}

func (f *fakeDiskManager) Measure(context.Context) error { f.measured++; return nil }

func TestDiskSettings_ReadByAnyoneWrittenOnlyByAnAdministrator(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	listener, admin := fixture.signIn(t, "listener"), fixture.signIn(t, "admin")

	recorder := fixture.do(authedGet(t, listener, "/api/v1/settings/disk"))
	var body struct {
		Inotify struct {
			Budget int64 `json:"budget_watches"`
		} `json:"inotify"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); recorder.Code != http.StatusOK || err != nil || body.Inotify.Budget != 500 {
		t.Fatalf("read: status %d budget %d err %v body %s", recorder.Code, body.Inotify.Budget, err, recorder.Body)
	}

	put := `{"groups":{"/mnt/media":"shared"}}`
	if recorder := fixture.do(authedPut(t, listener, "/api/v1/settings/disk", put)); recorder.Code != http.StatusForbidden {
		t.Fatalf("a listener's correction: status %d, want 403", recorder.Code)
	}
	if recorder := fixture.do(authedPut(t, admin, "/api/v1/settings/disk", put)); recorder.Code != http.StatusOK {
		t.Fatalf("an administrator's correction: status %d body %s", recorder.Code, recorder.Body)
	}
	if want := map[string]string{"/mnt/media": "shared"}; !reflect.DeepEqual(fixture.diskManager.overrides, want) {
		t.Fatalf("stored %v, want %v", fixture.diskManager.overrides, want)
	}
	if recorder := fixture.do(authedPost(t, listener, "/api/v1/settings/disk/measure", "")); recorder.Code != http.StatusForbidden || fixture.diskManager.measured != 0 {
		t.Fatalf("a listener must not probe disks: status %d measured %d", recorder.Code, fixture.diskManager.measured)
	}
	if recorder := fixture.do(authedPost(t, admin, "/api/v1/settings/disk/measure", "")); recorder.Code != http.StatusOK || fixture.diskManager.measured != 1 {
		t.Fatalf("measure: status %d measured %d, want 200 and 1", recorder.Code, fixture.diskManager.measured)
	}
}

func TestDiskScan_OnlyAnAdministratorSetsTheInterval(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	listener, admin := fixture.signIn(t, "listener"), fixture.signIn(t, "admin")

	if recorder := fixture.do(authedPut(t, listener, "/api/v1/settings/disk/scan", `{"interval_hours":12}`)); recorder.Code != http.StatusForbidden {
		t.Fatalf("a listener: status %d, want 403", recorder.Code)
	}
	if recorder := fixture.do(authedPut(t, admin, "/api/v1/settings/disk/scan", `{"interval_hours":12}`)); recorder.Code != http.StatusOK || fixture.diskManager.scanHours != 12 {
		t.Fatalf("an administrator: status %d hours %d, want 200 and 12", recorder.Code, fixture.diskManager.scanHours)
	}
}
