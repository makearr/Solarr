package apiserver

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
)

// GetMetadataSettings answers the metadata providers' own settings.
func (s *Server) GetMetadataSettings(w http.ResponseWriter, r *http.Request) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, s.metadataSettingsView())
}

// UpdateMetadataSettings changes them. Administrator only, written wholesale (ADR-44).
func (s *Server) UpdateMetadataSettings(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}

	var body gen.UpdateMetadataSettingsJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "expected brainzmash_user_agent")
		return
	}

	if err := s.auth.Registry.LookupAll(settings.KeyBrainzMashUserAgent); err != nil {
		writeError(w, s.logger, err)
		return
	}

	wanted := settings.MetadataSettings{BrainzMashUserAgent: body.BrainzmashUserAgent}
	if err := s.auth.Settings.SetMetadataSettings(r.Context(), clock.NewSystem(), wanted); err != nil {
		var problems config.Problems
		if errors.As(err, &problems) {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"The metadata settings have problems", problems.Report("the metadata settings", "Nothing was changed."))
			return
		}
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, s.metadataSettingsView())
}

func (s *Server) metadataSettingsView() gen.MetadataSettings {
	return gen.MetadataSettings{BrainzmashUserAgent: s.auth.Settings.Snapshot().MetadataSettings().BrainzMashUserAgent}
}
