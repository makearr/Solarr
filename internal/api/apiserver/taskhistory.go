package apiserver

import (
	"context"
	"net/http"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/jobs"
)

// TaskHourRow is the time one kind of job ran in one hour.
type TaskHourRow struct {
	JobType       string
	HourStartUnix int64
	RunMs, Runs   int64
}

// TaskHistory is what the workers did, hour by hour; cmd/solarr/adapters answers it from statistics.db.
type TaskHistory interface {
	Hours(ctx context.Context, since time.Time) ([]TaskHourRow, error)
}

// taskHistoryDays is how far back the scheduler's heatmap looks.
const taskHistoryDays = 7

// GetTaskHistory answers the last seven days of job run time, per hour and kind of job.
func (s *Server) GetTaskHistory(w http.ResponseWriter, r *http.Request) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	body := gen.TaskHistory{Hours: []gen.TaskHour{}}
	if s.taskHistory == nil {
		writeJSON(w, s.logger, http.StatusOK, body)
		return
	}
	since := s.auth.Clock.Now().Add(-taskHistoryDays * 24 * time.Hour).Truncate(time.Hour)
	rows, err := s.taskHistory.Hours(r.Context(), since)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	for _, row := range rows {
		name := row.JobType
		if s.jobDefs != nil {
			if definition, found := s.jobDefs.Lookup(jobs.Type(row.JobType)); found && definition.Name != "" {
				name = definition.Name
			}
		}
		body.Hours = append(body.Hours, gen.TaskHour{HourStart: row.HourStartUnix, JobType: row.JobType, Name: name, RunMs: row.RunMs, Runs: row.Runs})
	}
	writeJSON(w, s.logger, http.StatusOK, body)
}
