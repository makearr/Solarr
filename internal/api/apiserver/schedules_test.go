package apiserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/config"
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
	lastRunFull   bool
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

func (f *fakeScheduler) RunNow(_ context.Context, key string, full bool) error {
	if !containsTask(f.tasks, key) {
		return jobs.ErrUnknownTask
	}
	f.lastRunKey = key
	f.lastRunFull = full
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
	SupportsFull   bool   `json:"supports_full"`
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
	if schedules[0].SupportsFull {
		t.Error("supports_full = true for a task with no RunFull")
	}
}

// The screen needs supports_full to know whether to offer a "Full rescan" control at all --
// today only the task that actually registered RunFull should answer true.
func TestListSchedules_SupportsFullReflectsRunFull(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.scheduler.tasks = []jobs.Task{
		{Key: "scan", Name: "Library scan", DefaultCadence: "*/30 * * * *", Tier: "normal",
			RunFull: func(context.Context) (jobs.EnqueueResult, error) { return jobs.EnqueueResult{}, nil }},
		{Key: "enrichment", Name: "Music enrichment", DefaultCadence: "*/15 * * * *", Tier: "normal"},
	}
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
	byKey := map[string]bool{}
	for _, schedule := range schedules {
		byKey[schedule.Key] = schedule.SupportsFull
	}
	if !byKey["scan"] {
		t.Error("scan (has RunFull) supports_full = false, want true")
	}
	if byKey["enrichment"] {
		t.Error("enrichment (no RunFull) supports_full = true, want false")
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

// The full-rescan trigger (private/questions.md, todo 4.2's own follow-up): ?full=true must
// reach Scheduler.RunNow's own full argument, not just be accepted and ignored.
func TestRunSchedule_FullQueryParamIsPassedThrough(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.scheduler.tasks = []jobs.Task{{Key: "scan", DefaultCadence: "*/30 * * * *"}}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/schedules/scan/run?full=true", ``))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (%s)", recorder.Code, recorder.Body)
	}
	if !fixture.scheduler.lastRunFull {
		t.Error("full=true was not passed through to Scheduler.RunNow")
	}
}

// Without the query param, an ordinary run must not be mistaken for a full one.
func TestRunSchedule_OmittingFullRunsTheOrdinaryMode(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.scheduler.tasks = []jobs.Task{{Key: "scan", DefaultCadence: "*/30 * * * *"}}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/schedules/scan/run", ``))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (%s)", recorder.Code, recorder.Body)
	}
	if fixture.scheduler.lastRunFull {
		t.Error("an ordinary run (no ?full) was passed through as full")
	}
}

// A task with no RunFull (every task except scan) answers 400 for ?full=true, translated
// from jobs.ErrNoFullMode -- refused rather than silently running the ordinary mode instead.
func TestRunSchedule_MapsNoFullModeTo400(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.scheduler.tasks = []jobs.Task{{Key: "enrichment", DefaultCadence: "*/15 * * * *"}}
	fixture.scheduler.runNowErr = jobs.ErrNoFullMode
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/schedules/enrichment/run?full=true", ``))
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (%s)", recorder.Code, recorder.Body)
	}
}
