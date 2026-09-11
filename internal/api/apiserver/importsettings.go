package apiserver

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/makearr/MediaNexus/internal/api/apiserver/gen"
	"github.com/makearr/MediaNexus/internal/api/authz"
	"github.com/makearr/MediaNexus/internal/platform/clock"
	"github.com/makearr/MediaNexus/internal/platform/config"
)

// GetImportSettings answers ADR-48's own commit-line threshold, the same
// direct-settings.Cache shape GetNetworkSettings already uses -- import.
// auto_commit_confidence is a scalar policy value (notes.md §3), not
// behind its own adapter interface.
func (s *Server) GetImportSettings(w http.ResponseWriter, r *http.Request) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, s.importSettingsView())
}

// UpdateImportSettings changes the commit line. Administrator only, written
// wholesale like the network and cache settings (ADR-44).
func (s *Server) UpdateImportSettings(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}

	var body gen.UpdateImportSettingsJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "expected auto_commit_confidence")
		return
	}

	if err := s.auth.Settings.SetImportAutoCommitConfidence(r.Context(), clock.NewSystem(), float64(body.AutoCommitConfidence)); err != nil {
		var problems config.Problems
		if errors.As(err, &problems) {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"The import settings have problems", problems.Report("the import settings", "Nothing was changed."))
			return
		}
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, s.importSettingsView())
}

func (s *Server) importSettingsView() gen.ImportSettings {
	return gen.ImportSettings{
		AutoCommitConfidence: float32(s.auth.Settings.Snapshot().ImportAutoCommitConfidence()),
	}
}
