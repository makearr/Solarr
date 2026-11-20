package apiserver

import (
	"net/http"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
)

// GetDefaultLanguages answers the languages a new library prefers.
func (s *Server) GetDefaultLanguages(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	codes := s.auth.Settings.Snapshot().DefaultLanguages()
	if codes == nil {
		codes = []string{}
	}
	writeJSON(w, s.logger, http.StatusOK, gen.DefaultLanguages{Languages: codes})
}

// SetDefaultLanguages replaces them. Administrator only.
func (s *Server) SetDefaultLanguages(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	var body gen.DefaultLanguages
	if !s.decodeBody(w, r, maxImportBody, &body, "the request body could not be read") {
		return
	}
	if err := s.auth.Settings.SetDefaultLanguages(r.Context(), body.Languages); err != nil {
		s.writeTargetError(w, err)
		return
	}
	s.GetDefaultLanguages(w, r)
}
