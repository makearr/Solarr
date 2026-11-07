package apiserver

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/config"
)

func (s *Server) generalSettings() gen.GeneralSettings {
	snapshot := s.auth.Settings.Snapshot()
	hashing, gaps := snapshot.VideoPerceptualHash(), snapshot.HuntGaps()
	mode := gen.GeneralSettingsSolarrMode(snapshot.SolarrMode())
	return gen.GeneralSettings{VideoHashing: &hashing, HuntOldMissing: &gaps, SolarrMode: &mode}
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
	if body.SolarrMode != nil {
		err := s.auth.Settings.SetSolarrMode(r.Context(), s.auth.Clock, settings.SolarrMode(*body.SolarrMode))
		var problems config.Problems
		if errors.As(err, &problems) {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", problems.Report("the mode", "Nothing was changed."))
			return
		}
		if err != nil {
			writeError(w, s.logger, err)
			return
		}
	}
	writeJSON(w, s.logger, http.StatusOK, s.generalSettings())
}
