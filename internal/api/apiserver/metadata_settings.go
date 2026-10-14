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
		settings.KeyMetadataCoverSources, settings.KeyMetadataTVSources, settings.KeyMetadataWikidata, settings.KeyMetadataAcoustID,
		settings.KeyMetadataTheAudioDB, settings.KeyMetadataPeopleBackfillPerRun,
		settings.KeyAvailabilityGraceHigh, settings.KeyAvailabilityGraceMedium, settings.KeyAvailabilityGraceLow); err != nil {
		writeError(w, s.logger, err)
		return
	}

	wanted := settings.MetadataSettings{
		BrainzMashUserAgent: body.BrainzmashUserAgent, Wikidata: body.Wikidata, AcoustID: body.Acoustid,
		TheAudioDB: body.Theaudiodb, PeopleBackfillPerRun: body.PeopleBackfillPerRun,
	}
	if body.CoverSources != nil {
		wanted.CoverSources = make([]string, 0, len(*body.CoverSources))
		for _, source := range *body.CoverSources {
			wanted.CoverSources = append(wanted.CoverSources, string(source))
		}
	}
	if body.TvSources != nil {
		wanted.TVSources = make([]string, 0, len(*body.TvSources))
		for _, source := range *body.TvSources {
			wanted.TVSources = append(wanted.TVSources, string(source))
		}
	}
	if grace := body.AvailabilityGraceHours; grace != nil {
		wanted.AvailabilityGrace = &settings.AvailabilityGraceHours{High: grace.High, Medium: grace.Medium, Low: grace.Low}
	}
	wasGrace := s.auth.Settings.Snapshot().AvailabilityGrace()
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
	// A new wait counts at once: the re-count stamps it on every part.
	if wanted.AvailabilityGrace != nil && *wanted.AvailabilityGrace != wasGrace && s.scheduler != nil {
		if err := s.scheduler.RunNow(r.Context(), comingOutTaskKey, false); err != nil {
			s.logger.Warn("could not re-count what has come out after the wait changed", "error", err)
		}
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
	tvSources := make([]gen.MetadataSettingsTvSources, 0, len(stored.TVSources))
	for _, source := range stored.TVSources {
		tvSources = append(tvSources, gen.MetadataSettingsTvSources(source))
	}
	return gen.MetadataSettings{
		BrainzmashUserAgent: stored.BrainzMashUserAgent, SourceOrder: &order, TvSources: &tvSources,
		CoverSources: &covers, Wikidata: stored.Wikidata, Acoustid: stored.AcoustID,
		AcoustidApiKey:       &gen.SecretMask{HasValue: s.auth.Settings.Snapshot().AcoustIDConfigured()},
		Theaudiodb:           stored.TheAudioDB,
		TheaudiodbApiKey:     &gen.SecretMask{HasValue: s.auth.Settings.Snapshot().TheAudioDBConfigured()},
		TmdbApiKey:           &gen.SecretMask{HasValue: s.auth.Settings.Snapshot().TMDBConfigured()},
		TvdbApiKey:           &gen.SecretMask{HasValue: s.auth.Settings.Snapshot().TVDBConfigured()},
		PeopleBackfillPerRun: stored.PeopleBackfillPerRun,
		AvailabilityGraceHours: &struct {
			High   int64 `json:"high"`
			Low    int64 `json:"low"`
			Medium int64 `json:"medium"`
		}{High: stored.AvailabilityGrace.High, Low: stored.AvailabilityGrace.Low, Medium: stored.AvailabilityGrace.Medium},
	}
}

// comingOutTaskKey is the registered re-count of what has come out.
const comingOutTaskKey = "coming_out"

// SetTheAudioDBAPIKey replaces or clears an own TheAudioDB key. Administrator only.
func (s *Server) SetTheAudioDBAPIKey(w http.ResponseWriter, r *http.Request) {
	s.setProviderKey(w, r, settings.KeyTheAudioDBAPIKey, s.auth.Settings.SetTheAudioDBAPIKey)
}

// SetTMDBAPIKey replaces or clears the user's own TMDB credential. Administrator only.
func (s *Server) SetTMDBAPIKey(w http.ResponseWriter, r *http.Request) {
	s.setProviderKey(w, r, settings.KeyTMDBAPIKey, s.auth.Settings.SetTMDBAPIKey)
}

// SetTVDBAPIKey replaces or clears an own TheTVDB key. Administrator only.
func (s *Server) SetTVDBAPIKey(w http.ResponseWriter, r *http.Request) {
	s.setProviderKey(w, r, settings.KeyTVDBAPIKey, s.auth.Settings.SetTVDBAPIKey)
}

func (s *Server) setProviderKey(
	w http.ResponseWriter, r *http.Request, settingKey string,
	store func(context.Context, clock.Clock, secrets.Secret) error,
) {
	if !s.requireAdministrator(w, r) {
		return
	}
	var body struct {
		APIKey *string `json:"api_key"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "expected a JSON object")
		return
	}
	if err := s.auth.Registry.LookupAll(settingKey); err != nil {
		writeError(w, s.logger, err)
		return
	}
	key := secrets.Secret(strings.TrimSpace(derefOr(body.APIKey, "")))
	if err := store(r.Context(), clock.NewSystem(), key); err != nil {
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
