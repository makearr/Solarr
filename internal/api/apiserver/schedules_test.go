package apiserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/config"
)

// fakeScheduler is apiserver.Scheduler over in-memory maps, so a test can
// assert dispatch and error mapping without a real internal/jobs.TaskRegistry.
type fakeScheduler struct {
	tasks    []jobs.Task
	states   map[string]jobs.ScheduleRun
	cadences map[string][]string
	disabled map[string]bool
	startup  map[string]bool
	paused   map[string]bool

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

func (f *fakeScheduler) EffectiveCadences(key, defaultCadence string) []string {
	if override, found := f.cadences[key]; found {
		return override
	}
	return []string{defaultCadence}
}

func (f *fakeScheduler) RunAtStartup(key string, fallback bool) bool {
	if run, found := f.startup[key]; found {
		return run
	}
	return fallback
}

func (f *fakeScheduler) SetRunAtStartup(_ context.Context, key string, run bool) error {
	if f.startup == nil {
		f.startup = map[string]bool{}
	}
	f.startup[key] = run
	return nil
}

func (f *fakeScheduler) Enabled(key string) bool { return !f.disabled[key] }

func (f *fakeScheduler) SetCadences(_ context.Context, key string, cadences []string) error {
	if f.setCadenceErr != nil {
		return f.setCadenceErr
	}
	if f.cadences == nil {
		f.cadences = map[string][]string{}
	}
	f.cadences[key] = cadences
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

func (f *fakeScheduler) Pause(_ context.Context, key string) error {
	if f.paused == nil {
		f.paused = map[string]bool{}
	}
	f.paused[key] = true
	return nil
}

func (f *fakeScheduler) Resume(_ context.Context, key string) error {
	delete(f.paused, key)
	return nil
}

func (f *fakeScheduler) PauseState(key string) apiserver.PauseState {
	return apiserver.PauseState{Paused: f.paused[key] || f.paused[jobs.AllTasks], ByAll: f.paused[jobs.AllTasks]}
}

func (f *fakeScheduler) HeldJobs(context.Context) (map[string]int64, error) { return nil, nil }

func containsTask(tasks []jobs.Task, key string) bool {
	for _, task := range tasks {
		if task.Key == key {
			return true
		}
	}
	return false
}

type scheduleBody struct {
	Key            string   `json:"key"`
	Name           string   `json:"name"`
	Tier           string   `json:"tier"`
	DefaultCadence string   `json:"default_cadence"`
	Cadences       []string `json:"cadences"`
	Enabled        bool     `json:"enabled"`
	SupportsFull   bool     `json:"supports_full"`
	LastOutcome    string   `json:"last_outcome"`
	Category       string   `json:"category"`
	RunAtStartup   bool     `json:"run_at_startup"`
	Paused         bool     `json:"paused"`
	PausedByAll    bool     `json:"paused_by_all"`
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
	if len(schedules) != 1 || schedules[0].Key != "scan" || !reflect.DeepEqual(schedules[0].Cadences, []string{"*/30 * * * *"}) ||
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

	recorder := fixture.do(authedPatch(t, cookies, "/api/v1/schedules/scan", `{"cadences":["0 11 * * *","0 23 * * *"]}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var updated scheduleBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &updated); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(updated.Cadences, []string{"0 11 * * *", "0 23 * * *"}) || !updated.Enabled {
		t.Errorf("updated = %+v, want cadence overridden and enabled left alone", updated)
	}

	recorder = fixture.do(authedPatch(t, cookies, "/api/v1/schedules/scan", `{"enabled":false}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &updated); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if updated.Enabled || len(updated.Cadences) != 2 {
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

	recorder := fixture.do(authedPatch(t, cookies, "/api/v1/schedules/scan", `{"cadences":["nonsense"]}`))
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

// Every task can be set to run at startup: one that does by default can be switched off, one that does not, on.
func TestUpdateSchedule_RunAtStartupCanBeSetOnEveryTask(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.scheduler.tasks = []jobs.Task{
		{Key: "scan", DefaultCadence: "*/30 * * * *", Category: jobs.CategoryLibrary, Startup: jobs.StartupOn},
		{Key: "backup", DefaultCadence: "0 1 * * *", Category: jobs.CategoryMaintenance},
	}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	updated := map[string]scheduleBody{}
	for key, body := range map[string]string{"scan": `{"run_at_startup":false}`, "backup": `{"run_at_startup":true}`} {
		recorder := fixture.do(authedPatch(t, cookies, "/api/v1/schedules/"+key, body))
		var answered scheduleBody
		if err := json.Unmarshal(recorder.Body.Bytes(), &answered); err != nil {
			t.Fatalf("unmarshal: %v (%s)", err, recorder.Body)
		}
		updated[key] = answered
	}
	want := map[string]scheduleBody{
		"scan": {Key: "scan", DefaultCadence: "*/30 * * * *", Cadences: []string{"*/30 * * * *"},
			Enabled: true, Category: "library", RunAtStartup: false},
		"backup": {Key: "backup", DefaultCadence: "0 1 * * *", Cadences: []string{"0 1 * * *"},
			Enabled: true, Category: "maintenance", RunAtStartup: true},
	}
	if !reflect.DeepEqual(updated, want) {
		t.Errorf("updated = %+v\nwant      %+v", updated, want)
	}
}

func TestUpdateSchedule_PausesAndResumesOneTask(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.scheduler.tasks = []jobs.Task{{Key: "scan", DefaultCadence: "*/30 * * * *"}, {Key: "probe", DefaultCadence: "*/30 * * * *"}}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPatch(t, cookies, "/api/v1/schedules/scan", `{"paused":true}`))
	var updated scheduleBody
	if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &updated) != nil || !updated.Paused || updated.PausedByAll {
		t.Fatalf("pause: status = %d, body = %s, want scan paused on its own", recorder.Code, recorder.Body)
	}
	if fixture.scheduler.paused["probe"] {
		t.Error("pausing scan paused probe too")
	}

	recorder = fixture.do(authedPatch(t, cookies, "/api/v1/schedules/scan", `{"paused":false}`))
	if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &updated) != nil || updated.Paused {
		t.Errorf("resume: status = %d, body = %s, want scan running again", recorder.Code, recorder.Body)
	}
}

func TestSetAllSchedulesPaused_HoldsEveryTaskAndNeedsAnAdministrator(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.scheduler.tasks = []jobs.Task{{Key: "scan", DefaultCadence: "*/30 * * * *"}}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	fixture.createUser(t, "reader", settings.RoleUser)

	reader := fixture.signIn(t, "reader")
	if recorder := fixture.do(authedPut(t, reader, "/api/v1/schedules/paused", `{"paused":true}`)); recorder.Code != http.StatusForbidden {
		t.Errorf("status for a non-administrator = %d, want 403", recorder.Code)
	}

	admin := fixture.signIn(t, "admin")
	if recorder := fixture.do(authedPut(t, admin, "/api/v1/schedules/paused", `{"paused":true}`)); recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var listed []scheduleBody
	recorder := fixture.do(authedGet(t, admin, "/api/v1/schedules"))
	if err := json.Unmarshal(recorder.Body.Bytes(), &listed); err != nil || len(listed) != 1 || !listed[0].Paused || !listed[0].PausedByAll {
		t.Errorf("listed = %+v (%v), want scan paused by the every-task pause", listed, err)
	}

	if recorder := fixture.do(authedPut(t, admin, "/api/v1/schedules/paused", `{"paused":false}`)); recorder.Code != http.StatusNoContent {
		t.Fatalf("resume status = %d", recorder.Code)
	}
	if fixture.scheduler.paused[jobs.AllTasks] {
		t.Error("the every-task pause is still in place")
	}
}

func TestRunSchedule_MapsAPausedTaskTo409(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.scheduler.tasks = []jobs.Task{{Key: "scan", DefaultCadence: "*/30 * * * *"}}
	fixture.scheduler.runNowErr = jobs.ErrTaskPaused
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	if recorder := fixture.do(authedPost(t, cookies, "/api/v1/schedules/scan/run", ``)); recorder.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 (%s)", recorder.Code, recorder.Body)
	}
}
