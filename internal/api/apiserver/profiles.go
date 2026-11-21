package apiserver

import (
	"errors"
	"net/http"

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
	if !s.decodeBody(w, r, maxImportBody, &body, "the request body could not be read") {
		return
	}

	created, err := s.auth.Settings.CreateQualityProfileFor(r.Context(), derefOr(body.MediaType, settings.UnstatedProfileMediaType),
		body.Name, body.Rules, body.Upgrade)
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

// ValidateQualityProfile answers the mistakes a save would refuse the profile for, so the editor can check before saving.
func (s *Server) ValidateQualityProfile(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	var body gen.ValidateQualityProfileJSONRequestBody
	if !s.decodeBody(w, r, maxImportBody, &body, "the request body could not be read") {
		return
	}
	found := settings.ValidateQualityProfile(body.Name, body.Rules, body.Upgrade)
	answer := gen.QualityProfileCheck{Problems: make([]struct {
		Expectation string `json:"expectation"`
		Path        string `json:"path"`
		Value       string `json:"value"`
	}, len(found))}
	for index, problem := range found {
		answer.Problems[index].Path, answer.Problems[index].Value, answer.Problems[index].Expectation = problem.Path, problem.Value, problem.Expectation
	}
	writeJSON(w, s.logger, http.StatusOK, answer)
}

// UpdateQualityProfile replaces a profile's policy. Administrator only.
func (s *Server) UpdateQualityProfile(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}

	var body gen.UpdateQualityProfileJSONRequestBody
	if !s.decodeBody(w, r, maxImportBody, &body, "the request body could not be read") {
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

// DeleteQualityProfile removes a profile, refused (settings.ErrInUse, via
// writeError) while any category still defaults to it. Administrator only.
func (s *Server) DeleteQualityProfile(w http.ResponseWriter, r *http.Request, id int64, params gen.DeleteQualityProfileParams) {
	if !s.requireAdministrator(w, r) {
		return
	}
	remove := func() error { return s.auth.Settings.DeleteQualityProfile(r.Context(), id) }
	if params.ReplaceWith != nil {
		remove = func() error { return s.auth.Settings.ReplaceQualityProfile(r.Context(), id, *params.ReplaceWith) }
	}
	if err := remove(); err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func qualityProfileView(p settings.QualityProfile) gen.QualityProfile {
	return gen.QualityProfile{Id: p.ID, Name: p.Name, Rules: p.RulesJSON, Upgrade: p.UpgradeJSON, MediaType: p.MediaType}
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
	if !s.decodeBody(w, r, maxImportBody, &body, "the request body could not be read") {
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

// UpdateLanguageProfile replaces a profile's policy. Administrator only.
func (s *Server) UpdateLanguageProfile(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}

	var body gen.UpdateLanguageProfileJSONRequestBody
	if !s.decodeBody(w, r, maxImportBody, &body, "the request body could not be read") {
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
