package apiserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/MediaNexus/internal/api/apiserver/gen"
	"github.com/makearr/MediaNexus/internal/api/authz"
	"github.com/makearr/MediaNexus/internal/jobs"
	"github.com/makearr/MediaNexus/internal/platform/config"
)

// Scheduler exposes ADR-59's task registry, live settings and run-state to
// the API, and a manual trigger. Defined here and implemented by a
// cmd/medianexus adapter over internal/jobs.TaskRegistry/ScheduleStates and
// internal/db/settings.Cache, the same reasoning WorksReader's own doc comment
// gives for every other reader interface in this package.
type Scheduler interface {
	// Tasks lists every registered task, sorted by key.
	Tasks() []jobs.Task
	// State answers one task's own persisted run-state. found is false for
	// a task that has never ticked yet.
	State(ctx context.Context, key string) (jobs.ScheduleRun, bool, error)
	// EffectiveCadence resolves a task's live cadence: a stored override, or
	// defaultCadence.
	EffectiveCadence(key, defaultCadence string) string
	// Enabled resolves a task's live enabled flag.
	Enabled(key string) bool
	// SetCadence overrides one task's cadence (ADR-44: validated on write).
	SetCadence(ctx context.Context, key, cadence string) error
	// SetEnabled enables or disables one task.
	SetEnabled(ctx context.Context, key string, enabled bool) error
	// RunNow fires a task immediately. Returns jobs.ErrUnknownTask for an
	// unregistered key, jobs.ErrAlreadyRunning if a fire -- scheduled or
	// manual -- is already in flight.
	RunNow(ctx context.Context, key string) error
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
			"Bad request", "expected a JSON object with cadence and/or enabled")
		return
	}

	task := s.findTask(key)
	if task.Key == "" {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such task")
		return
	}

	if body.Cadence != nil {
		if err := s.scheduler.SetCadence(r.Context(), key, *body.Cadence); err != nil {
			var problems config.Problems
			if errors.As(err, &problems) {
				writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
					"The cadence has problems", problems.Report("the schedule", "Nothing was changed."))
				return
			}
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

// routeRunSchedule parses the key path parameter, mirroring routeGetWork.
func (s *Server) routeRunSchedule(w http.ResponseWriter, r *http.Request) {
	s.RunSchedule(w, r, chi.URLParam(r, "key"))
}

// RunSchedule fires a task immediately (todo 4.8's "trigger a scan, a probe
// sweep or a metadata refresh from the interface"). Administrator only.
// 202: the trigger was accepted, not that the underlying job has finished.
func (s *Server) RunSchedule(w http.ResponseWriter, r *http.Request, key string) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.scheduler == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "the scheduler is not available")
		return
	}

	if err := s.scheduler.RunNow(r.Context(), key); err != nil {
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

	view := gen.Schedule{
		Key: task.Key, Name: task.Name, Tier: gen.ScheduleTier(task.Tier),
		DefaultCadence: task.DefaultCadence,
		Cadence:        s.scheduler.EffectiveCadence(task.Key, task.DefaultCadence),
		Enabled:        s.scheduler.Enabled(task.Key),
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
