package apiserver

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/config"
)

// Scheduler exposes ADR-59's task registry, settings, run state and manual trigger; implemented in cmd/solarr over jobs and settings.Cache.
type Scheduler interface {
	// Tasks lists every registered task, sorted by key.
	Tasks() []jobs.Task
	// State answers one task's own persisted run-state. found is false for
	// a task that has never ticked yet.
	State(ctx context.Context, key string) (jobs.ScheduleRun, bool, error)
	// AllStates answers the run-state of every task that has ticked, by key, in one read.
	AllStates(ctx context.Context) (map[string]jobs.ScheduleRun, error)
	// EffectiveCadences resolves a task's live cadences: a stored override, or [defaultCadence].
	EffectiveCadences(key, defaultCadence string) []string
	// Enabled resolves a task's live enabled flag.
	Enabled(key string) bool
	// RunAtStartup resolves whether a task runs once at startup; fallback is its default.
	RunAtStartup(key string, fallback bool) bool
	// SetCadences overrides one task's cadences (ADR-44: validated on write).
	SetCadences(ctx context.Context, key string, cadences []string) error
	// SetRunAtStartup sets whether a task runs once at startup.
	SetRunAtStartup(ctx context.Context, key string, run bool) error
	// SetEnabled enables or disables one task.
	SetEnabled(ctx context.Context, key string, enabled bool) error
	// RunNow fires a task now; full requests its RunFull mode. Errors: ErrUnknownTask, ErrAlreadyRunning, ErrNoFullMode.
	RunNow(ctx context.Context, key string, full bool) error
	// Pause holds one task, or every task when key is jobs.AllTasks; Resume lifts it.
	Pause(ctx context.Context, key string) error
	Resume(ctx context.Context, key string) error
	// PauseState answers a task's hold: Paused is in effect, ByAll is the every-task pause, Since is when it began.
	PauseState(key string) PauseState
	// HeldJobs counts the jobs waiting in the queue per task key.
	HeldJobs(ctx context.Context) (map[string]int64, error)
}

// PauseState is what a task's hold looks like on the scheduler screen.
type PauseState struct {
	Paused bool
	ByAll  bool
	Since  *time.Time
}

// ListSchedules answers every registered task, its effective settings and
// its own run-state.
func (s *Server) ListSchedules(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.scheduler == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "the scheduler is not available")
		return
	}

	held, err := s.scheduler.HeldJobs(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	states, err := s.scheduler.AllStates(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	tasks := s.scheduler.Tasks()
	views := make([]gen.Schedule, len(tasks))
	for index, task := range tasks {
		views[index] = s.scheduleView(task, states[task.Key], held)
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}

// UpdateSchedule overrides a task's cadence and/or enabled flag.
// Administrator only. Both fields are independent -- omitting one leaves it
// unchanged.
func (s *Server) UpdateSchedule(w http.ResponseWriter, r *http.Request, key string) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.scheduler == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "the scheduler is not available")
		return
	}

	var body gen.UpdateScheduleJSONRequestBody
	if !s.decodeBody(w, r, maxSetupBody, &body, "expected a JSON object with cadences, enabled and/or run_at_startup") {
		return
	}

	task := s.findTask(key)
	if task.Key == "" {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such task")
		return
	}

	if body.Cadences != nil {
		if err := s.scheduler.SetCadences(r.Context(), key, *body.Cadences); err != nil {
			var problems config.Problems
			if errors.As(err, &problems) {
				writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
					"The schedule has problems", problems.Report("the schedule", "Nothing was changed."))
				return
			}
			writeError(w, s.logger, err)
			return
		}
	}
	if body.RunAtStartup != nil {
		if err := s.scheduler.SetRunAtStartup(r.Context(), key, *body.RunAtStartup); err != nil {
			writeError(w, s.logger, err)
			return
		}
	}
	if body.Paused != nil {
		pause := s.scheduler.Resume
		if *body.Paused {
			pause = s.scheduler.Pause
		}
		if err := pause(r.Context(), key); err != nil {
			writeError(w, s.logger, err)
			return
		}
	}
	if body.Enabled != nil {
		if err := s.scheduler.SetEnabled(r.Context(), key, *body.Enabled); err != nil {
			writeError(w, s.logger, err)
			return
		}
	}

	held, err := s.scheduler.HeldJobs(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	state, _, err := s.scheduler.State(r.Context(), key)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, s.scheduleView(task, state, held))
}

// routeRunSchedule parses the key path parameter and the full query string,
// mirroring routeListPeople's own manual gen.XParams construction.
func (s *Server) routeRunSchedule(w http.ResponseWriter, r *http.Request) {
	var params gen.RunScheduleParams
	if raw := r.URL.Query().Get("full"); raw != "" {
		full, err := strconv.ParseBool(raw)
		if err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"Bad request", "full must be true or false")
			return
		}
		params.Full = &full
	}
	s.RunSchedule(w, r, chi.URLParam(r, "key"), params)
}

// RunSchedule fires a task now (admin only); full=true asks for the full-run mode, 400 if none; 202 means accepted, not finished.
func (s *Server) RunSchedule(w http.ResponseWriter, r *http.Request, key string, params gen.RunScheduleParams) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.scheduler == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "the scheduler is not available")
		return
	}

	full := params.Full != nil && *params.Full
	if err := s.scheduler.RunNow(r.Context(), key, full); err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// SetAllSchedulesPaused pauses or resumes every task. Administrator only.
func (s *Server) SetAllSchedulesPaused(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.scheduler == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "the scheduler is not available")
		return
	}
	var body gen.SetAllSchedulesPausedJSONRequestBody
	if !s.decodeBody(w, r, maxSetupBody, &body, "expected a JSON object with paused") {
		return
	}
	pause := s.scheduler.Resume
	if body.Paused {
		pause = s.scheduler.Pause
	}
	if err := pause(r.Context(), jobs.AllTasks); err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) findTask(key string) jobs.Task {
	for _, task := range s.scheduler.Tasks() {
		if task.Key == key {
			return task
		}
	}
	return jobs.Task{}
}

func (s *Server) scheduleView(task jobs.Task, state jobs.ScheduleRun, held map[string]int64) gen.Schedule {
	view := gen.Schedule{
		Key: task.Key, Name: task.Name, Tier: gen.ScheduleTier(task.Tier),
		Category:       gen.ScheduleCategory(task.Category),
		DefaultCadence: task.DefaultCadence,
		Cadences:       s.scheduler.EffectiveCadences(task.Key, task.DefaultCadence),
		Enabled:        s.scheduler.Enabled(task.Key),
		SupportsFull:   task.RunFull != nil,
		RunAtStartup:   s.scheduler.RunAtStartup(task.Key, task.Startup == jobs.StartupOn),
		LastDurationMs: state.LastDurationMs, LastRunAt: state.LastRunAt, NextRunAt: state.NextRunAt,
	}
	pause := s.scheduler.PauseState(task.Key)
	view.Paused, view.PausedByAll, view.PausedAt, view.HeldJobs = pause.Paused, pause.ByAll, pause.Since, held[task.Key]
	if state.LastOutcome != "" {
		outcome := gen.ScheduleLastOutcome(state.LastOutcome)
		view.LastOutcome = &outcome
	}
	if state.LastError != "" {
		lastError := state.LastError
		view.LastError = &lastError
	}
	return view
}
