package apiserver

import (
	"context"
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

	if err := s.auth.Registry.LookupAll(settings.KeyBrainzMashUserAgent, settings.KeyMetadataSourceOrder,
		settings.KeyMetadataCoverSources, settings.KeyMetadataWikidata, settings.KeyMetadataAcoustID); err != nil {
		writeError(w, s.logger, err)
		return
	}

	wanted := settings.MetadataSettings{
		BrainzMashUserAgent: body.BrainzmashUserAgent, Wikidata: body.Wikidata, AcoustID: body.Acoustid,
	}
	if body.CoverSources != nil {
		wanted.CoverSources = make([]string, 0, len(*body.CoverSources))
		for _, source := range *body.CoverSources {
			wanted.CoverSources = append(wanted.CoverSources, string(source))
		}
	}
	wasAcoustID := *s.auth.Settings.Snapshot().MetadataSettings().AcoustID
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
	// Switched back on with a key in place: what it skipped while off is identified now (D12).
	if body.Acoustid != nil && *body.Acoustid && !wasAcoustID && s.fingerprintRetrier != nil &&
		s.auth.Settings.Snapshot().AcoustIDConfigured() {
		if _, err := s.fingerprintRetrier.RetryFingerprintSkipped(r.Context()); err != nil {
			s.logger.Warn("could not re-run what AcoustID skipped while switched off", "error", err)
		}
	}
	writeJSON(w, s.logger, http.StatusOK, s.metadataSettingsView())
}

// FingerprintRetrier re-runs the files the fingerprint pass skipped while no
// AcoustID key was configured (D12). Defined here and implemented by the caller,
// the same reasoning every other engine-backed interface in this package gives.
type FingerprintRetrier interface {
	RetryFingerprintSkipped(ctx context.Context) (directories int, err error)
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
	hadKey := s.auth.Settings.Snapshot().AcoustIDConfigured()
	if err := s.auth.Settings.SetAcoustIDAPIKey(r.Context(), clock.NewSystem(), key); err != nil {
		if errors.Is(err, settings.ErrSecretsUnavailable) {
			writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
				"Unavailable", "no encryption key is configured; an api key cannot be stored")
			return
		}
		writeError(w, s.logger, err)
		return
	}

	// D12: the files an absent key made the fingerprint pass skip are recorded as
	// such, and a key arriving is what re-runs them. Reported, never fatal -- the
	// key is stored either way, and a full rescan still recovers them.
	if key != "" && !hadKey && s.fingerprintRetrier != nil {
		if directories, err := s.fingerprintRetrier.RetryFingerprintSkipped(r.Context()); err != nil {
			s.logger.Warn("could not re-run what a missing AcoustID key skipped", "error", err)
		} else if directories > 0 {
			s.logger.Info("re-running what a missing AcoustID key skipped", "directories", directories)
		}
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
	covers := make([]gen.MetadataSettingsCoverSources, 0, len(stored.CoverSources))
	for _, source := range stored.CoverSources {
		covers = append(covers, gen.MetadataSettingsCoverSources(source))
	}
	return gen.MetadataSettings{
		BrainzmashUserAgent: stored.BrainzMashUserAgent, SourceOrder: &order,
		CoverSources: &covers, Wikidata: stored.Wikidata, Acoustid: stored.AcoustID,
		AcoustidApiKey: &gen.SecretMask{HasValue: s.auth.Settings.Snapshot().AcoustIDConfigured()},
	}
}
