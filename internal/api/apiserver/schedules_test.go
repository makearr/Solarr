package apiserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/makearr/MediaNexus/internal/db/settings"
	"github.com/makearr/MediaNexus/internal/jobs"
	"github.com/makearr/MediaNexus/internal/platform/config"
)

// fakeScheduler is apiserver.Scheduler over in-memory maps, so a test can
// assert dispatch and error mapping without a real internal/jobs.TaskRegistry.
type fakeScheduler struct {
	tasks    []jobs.Task
	states   map[string]jobs.ScheduleRun
	cadences map[string]string
	disabled map[string]bool

	setCadenceErr error
	runNowErr     error
	lastRunKey    string
}

func (f *fakeScheduler) Tasks() []jobs.Task { return f.tasks }

func (f *fakeScheduler) State(_ context.Context, key string) (jobs.ScheduleRun, bool, error) {
	run, found := f.states[key]
	return run, found, nil
}

func (f *fakeScheduler) EffectiveCadence(key, defaultCadence string) string {
	if override, found := f.cadences[key]; found {
		return override
	}
	return defaultCadence
}

func (f *fakeScheduler) Enabled(key string) bool { return !f.disabled[key] }

func (f *fakeScheduler) SetCadence(_ context.Context, key, cadence string) error {
	if f.setCadenceErr != nil {
		return f.setCadenceErr
	}
	if f.cadences == nil {
		f.cadences = map[string]string{}
	}
	f.cadences[key] = cadence
	return nil
}

func (f *fakeScheduler) SetEnabled(_ context.Context, key string, enabled bool) error {
	if f.disabled == nil {
		f.disabled = map[string]bool{}
	}
	f.disabled[key] = !enabled
	return nil
}

func (f *fakeScheduler) RunNow(_ context.Context, key string) error {
	if !containsTask(f.tasks, key) {
		return jobs.ErrUnknownTask
	}
	f.lastRunKey = key
	return f.runNowErr
}

func containsTask(tasks []jobs.Task, key string) bool {
	for _, task := range tasks {
		if task.Key == key {
			return true
		}
	}
	return false
}

type scheduleBody struct {
	Key            string `json:"key"`
	Name           string `json:"name"`
	Tier           string `json:"tier"`
	DefaultCadence string `json:"default_cadence"`
	Cadence        string `json:"cadence"`
	Enabled        bool   `json:"enabled"`
	LastOutcome    string `json:"last_outcome"`
}

func TestListSchedules_ReturnsEveryTaskWithItsEffectiveSettings(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.scheduler.tasks = []jobs.Task{
		{Key: "scan", Name: "Library scan", DefaultCadence: "*/30 * * * *", Tier: "normal"},
	}
	fixture.scheduler.states["scan"] = jobs.ScheduleRun{TaskKey: "scan", LastOutcome: "enqueued"}
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/schedules"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var schedules []scheduleBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &schedules); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(schedules) != 1 || schedules[0].Key != "scan" || schedules[0].Cadence != "*/30 * * * *" ||
		!schedules[0].Enabled || schedules[0].LastOutcome != "enqueued" {
		t.Errorf("schedules = %+v", schedules)
	}
}

func TestListSchedules_RequiresAuthentication(t *testing.T) {
	fixture := newWorksFixture(t)
	recorder := fixture.do(authedGet(t, nil, "/api/v1/schedules"))
	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", recorder.Code)
	}
}

func TestUpdateSchedule_OverridesCadenceAndEnabledIndependently(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.scheduler.tasks = []jobs.Task{{Key: "scan", Name: "Library scan", DefaultCadence: "*/30 * * * *"}}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPatch(t, cookies, "/api/v1/schedules/scan", `{"cadence":"0 * * * *"}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var updated scheduleBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &updated); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if updated.Cadence != "0 * * * *" || !updated.Enabled {
		t.Errorf("updated = %+v, want cadence overridden and enabled left alone", updated)
	}

	recorder = fixture.do(authedPatch(t, cookies, "/api/v1/schedules/scan", `{"enabled":false}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &updated); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if updated.Enabled || updated.Cadence != "0 * * * *" {
		t.Errorf("updated = %+v, want disabled with the cadence override still in place", updated)
	}
}

func TestUpdateSchedule_RequiresAdministrator(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.scheduler.tasks = []jobs.Task{{Key: "scan", DefaultCadence: "*/30 * * * *"}}
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedPatch(t, cookies, "/api/v1/schedules/scan", `{"enabled":false}`))
	if recorder.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", recorder.Code)
	}
}

func TestUpdateSchedule_UnknownTaskAnswers404(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPatch(t, cookies, "/api/v1/schedules/does-not-exist", `{"enabled":false}`))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (%s)", recorder.Code, recorder.Body)
	}
}

func TestUpdateSchedule_MapsAnInvalidCadenceTo400(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.scheduler.tasks = []jobs.Task{{Key: "scan", DefaultCadence: "*/30 * * * *"}}
	fixture.scheduler.setCadenceErr = config.Problems{{Path: "cadence", Expectation: "a valid five-field cron expression"}}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPatch(t, cookies, "/api/v1/schedules/scan", `{"cadence":"nonsense"}`))
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (%s)", recorder.Code, recorder.Body)
	}
}

func TestRunSchedule_FiresAndAnswers202(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.scheduler.tasks = []jobs.Task{{Key: "scan", DefaultCadence: "*/30 * * * *"}}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/schedules/scan/run", ``))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (%s)", recorder.Code, recorder.Body)
	}
	if fixture.scheduler.lastRunKey != "scan" {
		t.Errorf("lastRunKey = %q, want scan", fixture.scheduler.lastRunKey)
	}
}

func TestRunSchedule_RequiresAdministrator(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.scheduler.tasks = []jobs.Task{{Key: "scan", DefaultCadence: "*/30 * * * *"}}
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/schedules/scan/run", ``))
	if recorder.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", recorder.Code)
	}
}

func TestRunSchedule_MapsAlreadyRunningTo409(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.scheduler.tasks = []jobs.Task{{Key: "scan", DefaultCadence: "*/30 * * * *"}}
	fixture.scheduler.runNowErr = jobs.ErrAlreadyRunning
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/schedules/scan/run", ``))
	if recorder.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 (%s)", recorder.Code, recorder.Body)
	}
}

func TestRunSchedule_UnknownTaskAnswers404(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/schedules/does-not-exist/run", ``))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (%s)", recorder.Code, recorder.Body)
	}
}
