package apiserver

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/config"
)

// GetRecycleBinLimits answers the recycle bin's space bound.
func (s *Server) GetRecycleBinLimits(w http.ResponseWriter, r *http.Request) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, recycleBinLimitsView(s.auth.Settings.Snapshot().RecycleBinLimits()))
}

// UpdateRecycleBinLimits changes the recycle bin's space bound. Administrator only.
func (s *Server) UpdateRecycleBinLimits(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	var body gen.RecycleBinLimits
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "expected max_bytes and max_percent_of_root")
		return
	}
	if err := s.auth.Registry.LookupAll(settings.KeyRecycleBinMaxBytes, settings.KeyRecycleBinMaxPercentOfRoot); err != nil {
		writeError(w, s.logger, err)
		return
	}
	limits := settings.RecycleBinLimits{MaxBytes: body.MaxBytes, MaxPercentOfRoot: body.MaxPercentOfRoot}
	if err := s.auth.Settings.SetRecycleBinLimits(r.Context(), s.auth.Clock, limits); err != nil {
		var problems config.Problems
		if errors.As(err, &problems) {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"The recycle bin limits have problems", problems.Report("the recycle bin limits", "Nothing was changed."))
			return
		}
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, recycleBinLimitsView(limits))
}

func recycleBinLimitsView(limits settings.RecycleBinLimits) gen.RecycleBinLimits {
	return gen.RecycleBinLimits{MaxBytes: limits.MaxBytes, MaxPercentOfRoot: limits.MaxPercentOfRoot}
}
