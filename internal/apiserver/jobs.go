package apiserver

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/makearr/MediaNexus/internal/authz"
	"github.com/makearr/MediaNexus/internal/jobs"
	"github.com/makearr/MediaNexus/internal/paginate"
	"github.com/makearr/MediaNexus/internal/settings"
)

// handleListJobs returns a page of jobs the caller may see.
//
// The handler does not filter: it converts an Access into a Visibility and
// hands that to the repository, which narrows the query (Invariant 36). A
// handler that filtered afterwards would return short pages and break the
// cursor, quite apart from being the kind of check that gets forgotten.
func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	cursor, err := paginate.Decode(r.URL.Query().Get("cursor"))
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", err.Error())
		return
	}
	limit, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", err.Error())
		return
	}
	state, err := parseState(r.URL.Query().Get("state"))
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", err.Error())
		return
	}

	// Availability is checked after the request is validated: a malformed
	// cursor is a bad request whether or not the queue happens to be running,
	// and answering 503 to it would send a client looking in the wrong place.
	if s.jobs == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "the job queue is not running")
		return
	}

	page, err := s.jobs.ListPage(r.Context(), visibilityFor(access), state, cursor, limit)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, paginate.Page[jobView]{
		Items: toJobViews(page.Items), NextCursor: page.NextCursor,
	})
}

// visibilityFor turns an identity into what it may see.
//
// System jobs -- a backup, a maintenance pass -- belong to no category, so no
// category grant can cover them. Who sees them is a role question, and only an
// administrator or operator does.
func visibilityFor(access authz.Access) jobs.Visibility {
	role := access.Role()
	return jobs.Visibility{
		Categories: access.VisibleCategoryIDs(settings.PermissionRead),
		System:     access.IsSystem() || role == settings.RoleAdmin || role == settings.RoleOperator,
	}
}

func parseLimit(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil {
		return 0, errors.New("limit must be a number")
	}
	return limit, nil
}

// parseState rejects an unknown state rather than silently returning nothing,
// which would look like an empty queue.
func parseState(raw string) (jobs.State, error) {
	switch state := jobs.State(raw); state {
	case "", jobs.StatePending, jobs.StatePendingRetry, jobs.StateLeased,
		jobs.StateRunning, jobs.StateSucceeded, jobs.StateFailed, jobs.StateCancelled:
		return state, nil
	default:
		return "", errors.New("unknown state: expected one of PENDING, PENDING_RETRY, " +
			"LEASED, RUNNING, SUCCEEDED, FAILED, CANCELLED")
	}
}

// jobView is the API's shape. The payload is deliberately absent: it can carry
// anything a job type puts there, including paths and provider identifiers, and
// a listing is not the place to hand that out.
type jobView struct {
	ID            int64  `json:"id"`
	Type          string `json:"type"`
	Pool          string `json:"pool"`
	State         string `json:"state"`
	Priority      int    `json:"priority"`
	Attempts      int    `json:"attempts"`
	MaxAttempts   int    `json:"max_attempts"`
	CategoryID    int64  `json:"category_id,omitempty"`
	CorrelationID string `json:"correlation_id,omitempty"`
	LastError     string `json:"last_error,omitempty"`
	RunAfter      string `json:"run_after"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

func toJobViews(from []jobs.Job) []jobView {
	views := make([]jobView, 0, len(from))
	for _, job := range from {
		views = append(views, jobView{
			ID: job.ID, Type: string(job.Type), Pool: string(job.Pool),
			State: string(job.State), Priority: int(job.Priority),
			Attempts: job.Attempts, MaxAttempts: job.MaxAttempts,
			CategoryID: job.CategoryID, CorrelationID: job.CorrelationID,
			LastError: job.LastError,
			RunAfter:  job.RunAfter.Format(timeFormat),
			CreatedAt: job.CreatedAt.Format(timeFormat),
			UpdatedAt: job.UpdatedAt.Format(timeFormat),
		})
	}
	return views
}
