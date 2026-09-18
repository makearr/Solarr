package apiserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
	"github.com/makearr/Solarr/internal/platform/secrets"
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

	if err := s.auth.Registry.LookupAll(settings.KeyBrainzMashUserAgent, settings.KeyMetadataSourceOrder); err != nil {
		writeError(w, s.logger, err)
		return
	}

	wanted := settings.MetadataSettings{BrainzMashUserAgent: body.BrainzmashUserAgent}
	// Absent leaves the stored order alone, so a client that only knows about
	// the agent cannot silently reset it.
	if body.SourceOrder != nil {
		order := make([]string, 0, len(*body.SourceOrder))
		for _, source := range *body.SourceOrder {
			order = append(order, string(source))
		}
		wanted.SourceOrder = order
	}
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

// SetAcoustIDAPIKey replaces or clears the AcoustID application key, administrator
// only. An empty key clears it, disabling the fingerprint rung rather than failing it (D12).
func (s *Server) SetAcoustIDAPIKey(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}

	var body gen.SetAcoustIDAPIKeyJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "expected a JSON object")
		return
	}
	if err := s.auth.Registry.LookupAll(settings.KeyAcoustIDAPIKey); err != nil {
		writeError(w, s.logger, err)
		return
	}

	key := secrets.Secret(strings.TrimSpace(derefOr(body.ApiKey, "")))
	if err := s.auth.Settings.SetAcoustIDAPIKey(r.Context(), clock.NewSystem(), key); err != nil {
		if errors.Is(err, settings.ErrSecretsUnavailable) {
			writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
				"Unavailable", "no encryption key is configured; an api key cannot be stored")
			return
		}
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) metadataSettingsView() gen.MetadataSettings {
	stored := s.auth.Settings.Snapshot().MetadataSettings()
	order := make([]gen.MetadataSource, 0, len(stored.SourceOrder))
	for _, source := range stored.SourceOrder {
		order = append(order, gen.MetadataSource(source))
	}
	// HasValue alone: whether a key exists is all a screen asking for one
	// needs, and AcoustIDConfigured answers it without decrypting anything.
	return gen.MetadataSettings{
		BrainzmashUserAgent: stored.BrainzMashUserAgent, SourceOrder: &order,
		AcoustidApiKey: &gen.SecretMask{HasValue: s.auth.Settings.Snapshot().AcoustIDConfigured()},
	}
}
