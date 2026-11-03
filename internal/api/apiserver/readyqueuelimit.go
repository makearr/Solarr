package apiserver

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/config"
)

// GetReadyQueueLimit answers how many releases are held while the download clients are full.
func (s *Server) GetReadyQueueLimit(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	writeJSON(w, s.logger, http.StatusOK, gen.ReadyQueueLimit{Limit: int(s.auth.Settings.Snapshot().ReadyQueueLimit())})
}

// SetReadyQueueLimit changes the bound; it applies to the next release held.
func (s *Server) SetReadyQueueLimit(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	var request gen.ReadyQueueLimit
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxUserBody)).Decode(&request); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "expected a JSON object with limit")
		return
	}
	if err := s.auth.Registry.LookupAll(settings.KeyReadyQueueLimit); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if err := s.auth.Settings.SetReadyQueueLimit(r.Context(), s.auth.Clock, int64(request.Limit)); err != nil {
		var problems config.Problems
		if errors.As(err, &problems) {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"The limit has problems", problems.Report("the held-release limit", "Nothing was changed."))
			return
		}
		writeError(w, s.logger, err)
		return
	}
	s.GetReadyQueueLimit(w, r)
}
