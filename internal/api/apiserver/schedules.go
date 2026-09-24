package apiserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/config"
)

// Scheduler exposes ADR-59's task registry, live settings and run-state to
// the API, and a manual trigger. Defined here and implemented by a
// cmd/solarr adapter over internal/jobs.TaskRegistry/ScheduleStates and
// internal/db/settings.Cache, the same reasoning WorksReader's own doc comment
// gives for every other reader interface in this package.
type Scheduler interface {
	// Tasks lists every registered task, sorted by key.
	Tasks() []jobs.Task
	// State answers one task's own persisted run-state. found is false for
	// a task that has never ticked yet.
	State(ctx context.Context, key string) (jobs.ScheduleRun, bool, error)
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
	// RunNow fires a task immediately. full requests the task's own RunFull
	// trigger instead of its ordinary Enqueue -- library scan's full rescan,
	// today's only user (private/questions.md). Returns jobs.ErrUnknownTask
	// for an unregistered key, jobs.ErrAlreadyRunning if a fire -- scheduled
	// or manual -- is already in flight, jobs.ErrNoFullMode if full is true
	// for a task that never registered one.
	RunNow(ctx context.Context, key string, full bool) error
}

// ListSchedules answers every registered task, its effective settings and
// its own run-state.
func (s *Server) ListSchedules(w http.ResponseWriter, r *http.Request) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.scheduler == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "the scheduler is not available")
		return
	}

	tasks := s.scheduler.Tasks()
	views := make([]gen.Schedule, len(tasks))
	for index, task := range tasks {
		view, err := s.scheduleView(r.Context(), task)
		if err != nil {
			writeError(w, s.logger, err)
			return
		}
		views[index] = view
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}

// routeUpdateSchedule parses the key path parameter, mirroring routeGetWork.
func (s *Server) routeUpdateSchedule(w http.ResponseWriter, r *http.Request) {
	s.UpdateSchedule(w, r, chi.URLParam(r, "key"))
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
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "expected a JSON object with cadences, enabled and/or run_at_startup")
		return
	}

	task := s.findTask(key)
	if task.Key == "" {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such task")
		return
	}

	if body.RunAtStartup != nil && task.Startup == jobs.StartupNotOffered {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "this task does not run at startup")
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
	if body.Enabled != nil {
		if err := s.scheduler.SetEnabled(r.Context(), key, *body.Enabled); err != nil {
			writeError(w, s.logger, err)
			return
		}
	}

	view, err := s.scheduleView(r.Context(), task)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, view)
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

// RunSchedule fires a task immediately (todo 4.8's "trigger a scan, a probe
// sweep or a metadata refresh from the interface"). Administrator only.
// full=true asks for the task's own full-run mode instead of its ordinary
// one -- library scan's full rescan (private/questions.md), refused with 400
// for any task that has none. 202: the trigger was accepted, not that the
// underlying job has finished.
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

func (s *Server) findTask(key string) jobs.Task {
	for _, task := range s.scheduler.Tasks() {
		if task.Key == key {
			return task
		}
	}
	return jobs.Task{}
}

func (s *Server) scheduleView(ctx context.Context, task jobs.Task) (gen.Schedule, error) {
	state, _, err := s.scheduler.State(ctx, task.Key)
	if err != nil {
		return gen.Schedule{}, err
	}

	offered := task.Startup != jobs.StartupNotOffered
	view := gen.Schedule{
		Key: task.Key, Name: task.Name, Tier: gen.ScheduleTier(task.Tier),
		Category:       gen.ScheduleCategory(task.Category),
		DefaultCadence: task.DefaultCadence,
		Cadences:       s.scheduler.EffectiveCadences(task.Key, task.DefaultCadence),
		Enabled:        s.scheduler.Enabled(task.Key),
		SupportsFull:   task.RunFull != nil,
		StartupOffered: offered,
		RunAtStartup:   offered && s.scheduler.RunAtStartup(task.Key, task.Startup == jobs.StartupOn),
		LastDurationMs: state.LastDurationMs, LastRunAt: state.LastRunAt, NextRunAt: state.NextRunAt,
	}
	if state.LastOutcome != "" {
		outcome := gen.ScheduleLastOutcome(state.LastOutcome)
		view.LastOutcome = &outcome
	}
	if state.LastError != "" {
		lastError := state.LastError
		view.LastError = &lastError
	}
	return view, nil
}
