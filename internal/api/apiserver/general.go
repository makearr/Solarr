package apiserver

import (
	"encoding/json"
	"net/http"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
)

func (s *Server) generalSettings() gen.GeneralSettings {
	snapshot := s.auth.Settings.Snapshot()
	hashing, gaps := snapshot.VideoPerceptualHash(), snapshot.HuntGaps()
	return gen.GeneralSettings{VideoHashing: &hashing, HuntOldMissing: &gaps}
}

// GetGeneralSettings answers the instance-wide switches of Settings -> General.
func (s *Server) GetGeneralSettings(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, s.logger, http.StatusOK, s.generalSettings())
}

// PatchGeneralSettings changes the switches the body names. Administrator only.
func (s *Server) PatchGeneralSettings(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	var body gen.GeneralSettings
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxUserBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "expected a JSON object")
		return
	}
	if body.VideoHashing != nil {
		if err := s.auth.Settings.SetVideoPerceptualHash(r.Context(), s.auth.Clock, *body.VideoHashing); err != nil {
			writeError(w, s.logger, err)
			return
		}
	}
	if body.HuntOldMissing != nil {
		if err := s.auth.Settings.SetHuntGaps(r.Context(), s.auth.Clock, *body.HuntOldMissing); err != nil {
			writeError(w, s.logger, err)
			return
		}
	}
	writeJSON(w, s.logger, http.StatusOK, s.generalSettings())
}
