package apiserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/config"
)

// ListQualityProfiles returns every ADR-40 quality profile. Unlike works or
// categories, a profile carries no per-category visibility of its own -- an
// authenticated caller sees the whole list, same as exclusion patterns.
func (s *Server) ListQualityProfiles(w http.ResponseWriter, r *http.Request) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	profiles := s.auth.Settings.Snapshot().QualityProfiles()
	views := make([]gen.QualityProfile, len(profiles))
	for i, p := range profiles {
		views[i] = qualityProfileView(p)
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}

// CreateQualityProfile adds a profile. Administrator only, matching every
// other settings-writing endpoint.
func (s *Server) CreateQualityProfile(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}

	var body gen.CreateQualityProfileJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "the request body could not be read")
		return
	}

	created, err := s.auth.Settings.CreateQualityProfile(r.Context(), body.Name, body.Rules, body.Upgrade)
	if err != nil {
		var problems config.Problems
		if errors.As(err, &problems) {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"The profile has problems", problems.Report("the profile", "Nothing was created."))
			return
		}
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusCreated, qualityProfileView(created))
}

// routeUpdateQualityProfile parses the id path parameter, mirroring routeUpdateExclusionPattern.
func (s *Server) routeUpdateQualityProfile(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "an id must be a number")
		return
	}
	s.UpdateQualityProfile(w, r, id)
}

// UpdateQualityProfile replaces a profile's policy. Administrator only.
func (s *Server) UpdateQualityProfile(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}

	var body gen.UpdateQualityProfileJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "the request body could not be read")
		return
	}

	if err := s.auth.Settings.UpdateQualityProfile(r.Context(), id, body.Name, body.Rules, body.Upgrade); err != nil {
		var problems config.Problems
		if errors.As(err, &problems) {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"The profile has problems", problems.Report("the profile", "Nothing was changed."))
			return
		}
		writeError(w, s.logger, err)
		return
	}

	updated, ok := s.auth.Settings.Snapshot().QualityProfile(id)
	if !ok {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such quality profile")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, qualityProfileView(updated))
}

// routeDeleteQualityProfile parses the id path parameter, mirroring routeDeleteExclusionPattern.
func (s *Server) routeDeleteQualityProfile(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "an id must be a number")
		return
	}
	s.DeleteQualityProfile(w, r, id)
}

// DeleteQualityProfile removes a profile, refused (settings.ErrInUse, via
// writeError) while any category still defaults to it. Administrator only.
func (s *Server) DeleteQualityProfile(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if err := s.auth.Settings.DeleteQualityProfile(r.Context(), id); err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func qualityProfileView(p settings.QualityProfile) gen.QualityProfile {
	return gen.QualityProfile{Id: p.ID, Name: p.Name, Rules: p.RulesJSON, Upgrade: p.UpgradeJSON}
}

// ListLanguageProfiles returns every section 19.8 language profile.
func (s *Server) ListLanguageProfiles(w http.ResponseWriter, r *http.Request) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	profiles := s.auth.Settings.Snapshot().LanguageProfiles()
	views := make([]gen.LanguageProfile, len(profiles))
	for i, p := range profiles {
		views[i] = languageProfileView(p)
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}

// CreateLanguageProfile adds a profile. Administrator only.
func (s *Server) CreateLanguageProfile(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}

	var body gen.CreateLanguageProfileJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "the request body could not be read")
		return
	}

	created, err := s.auth.Settings.CreateLanguageProfile(r.Context(), languageProfileFromWrite(body))
	if err != nil {
		var problems config.Problems
		if errors.As(err, &problems) {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"The profile has problems", problems.Report("the profile", "Nothing was created."))
			return
		}
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusCreated, languageProfileView(created))
}

// routeUpdateLanguageProfile parses the id path parameter, mirroring routeUpdateQualityProfile.
func (s *Server) routeUpdateLanguageProfile(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "an id must be a number")
		return
	}
	s.UpdateLanguageProfile(w, r, id)
}

// UpdateLanguageProfile replaces a profile's policy. Administrator only.
func (s *Server) UpdateLanguageProfile(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}

	var body gen.UpdateLanguageProfileJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "the request body could not be read")
		return
	}

	if err := s.auth.Settings.UpdateLanguageProfile(r.Context(), id, languageProfileFromWrite(body)); err != nil {
		var problems config.Problems
		if errors.As(err, &problems) {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"The profile has problems", problems.Report("the profile", "Nothing was changed."))
			return
		}
		writeError(w, s.logger, err)
		return
	}

	updated, ok := s.auth.Settings.Snapshot().LanguageProfile(id)
	if !ok {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such language profile")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, languageProfileView(updated))
}

// routeDeleteLanguageProfile parses the id path parameter, mirroring routeDeleteQualityProfile.
func (s *Server) routeDeleteLanguageProfile(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "an id must be a number")
		return
	}
	s.DeleteLanguageProfile(w, r, id)
}

// DeleteLanguageProfile removes a profile, refused (settings.ErrInUse, via
// writeError) while any category still defaults to it. Administrator only.
func (s *Server) DeleteLanguageProfile(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if err := s.auth.Settings.DeleteLanguageProfile(r.Context(), id); err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func languageProfileView(p settings.LanguageProfile) gen.LanguageProfile {
	return gen.LanguageProfile{
		Id: p.ID, Name: p.Name,
		AudioPreferred: nonNil(p.AudioPreferred), AudioRequired: nonNil(p.AudioRequired),
		SubtitlePreferred: nonNil(p.SubtitlePreferred), SubtitleRequired: nonNil(p.SubtitleRequired),
		PreferOriginalAudio: p.PreferOriginalAudio, RejectDubbed: p.RejectDubbed,
	}
}

func languageProfileFromWrite(body gen.LanguageProfileWrite) settings.LanguageProfile {
	return settings.LanguageProfile{
		Name:                body.Name,
		AudioPreferred:      derefOr(body.AudioPreferred, nil),
		AudioRequired:       derefOr(body.AudioRequired, nil),
		SubtitlePreferred:   derefOr(body.SubtitlePreferred, nil),
		SubtitleRequired:    derefOr(body.SubtitleRequired, nil),
		PreferOriginalAudio: derefOr(body.PreferOriginalAudio, false),
		RejectDubbed:        derefOr(body.RejectDubbed, false),
	}
}

// nonNil turns a nil slice into an empty one -- required array fields in the
// OpenAPI schema must serialize as [], never JSON null (PersonDetail's own
// Genres/Aliases lesson, 2026-09-12).
func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
