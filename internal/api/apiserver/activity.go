package apiserver

import (
	"net/http"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
)

// GetActivity answers the named jobs running now for the status bar; a library's job only to those
// who can see that library, system work to everyone.
func (s *Server) GetActivity(w http.ResponseWriter, r *http.Request) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	body := gen.Activity{Running: []gen.RunningJob{}}
	if s.running == nil || s.jobDefs == nil {
		writeJSON(w, s.logger, http.StatusOK, body)
		return
	}
	for _, job := range s.running.Snapshot() {
		if job.CategoryID != 0 && !access.CanSee(job.CategoryID) {
			continue
		}
		// Only work a person would recognise: an unnamed type is housekeeping that lasts a second.
		definition, found := s.jobDefs.Lookup(job.Type)
		if !found || definition.Name == "" {
			continue
		}
		view := gen.RunningJob{
			JobId: job.JobID, Type: string(job.Type), Name: definition.Name, StartedAt: job.StartedAt, Done: job.Done,
		}
		if job.Total > 0 {
			view.Total = &job.Total
		}
		if job.PerMinute > 0 {
			perMinute := float32(job.PerMinute)
			view.PerMinute = &perMinute
		}
		if job.Remaining > 0 {
			seconds := int64(job.Remaining.Seconds())
			view.EtaSeconds = &seconds
		}
		body.Running = append(body.Running, view)
	}
	writeJSON(w, s.logger, http.StatusOK, body)
}
