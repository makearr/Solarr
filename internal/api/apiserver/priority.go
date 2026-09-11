package apiserver

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/makearr/MediaNexus/internal/api/apiserver/gen"
	"github.com/makearr/MediaNexus/internal/api/authz"
	"github.com/makearr/MediaNexus/internal/db/settings"
	"github.com/makearr/MediaNexus/internal/platform/clock"
	"github.com/makearr/MediaNexus/internal/platform/config"
)

// GetPrioritySettings answers ADR-36's own trigger weights and ADR-55's
// freshness window, the same direct-settings.Cache shape GetNetworkSettings
// already uses.
func (s *Server) GetPrioritySettings(w http.ResponseWriter, r *http.Request) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, s.prioritySettingsView())
}

// UpdatePrioritySettings changes the weights or the freshness window.
// Administrator only, written wholesale like the network and cache
// settings (ADR-44).
func (s *Server) UpdatePrioritySettings(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}

	var body gen.UpdatePrioritySettingsJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "expected the four weights and new_release_days")
		return
	}

	if err := s.auth.Registry.LookupAll(
		settings.KeyPriorityUserRequestWeight, settings.KeyPriorityNewReleaseWeight,
		settings.KeyPriorityGapWeight, settings.KeyPriorityUpgradeWeight, settings.KeyPriorityNewReleaseDays,
	); err != nil {
		writeError(w, s.logger, err)
		return
	}

	wanted := settings.PrioritySettings{
		UserRequestWeight: body.UserRequestWeight, NewReleaseWeight: body.NewReleaseWeight,
		GapWeight: body.GapWeight, UpgradeWeight: body.UpgradeWeight, NewReleaseDays: body.NewReleaseDays,
	}
	if err := s.auth.Settings.SetPrioritySettings(r.Context(), clock.NewSystem(), wanted); err != nil {
		var problems config.Problems
		if errors.As(err, &problems) {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"The priority settings have problems", problems.Report("the priority settings", "Nothing was changed."))
			return
		}
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, s.prioritySettingsView())
}

func (s *Server) prioritySettingsView() gen.PrioritySettings {
	stored := s.auth.Settings.Snapshot().PrioritySettings()
	return gen.PrioritySettings{
		UserRequestWeight: stored.UserRequestWeight, NewReleaseWeight: stored.NewReleaseWeight,
		GapWeight: stored.GapWeight, UpgradeWeight: stored.UpgradeWeight, NewReleaseDays: stored.NewReleaseDays,
	}
}
