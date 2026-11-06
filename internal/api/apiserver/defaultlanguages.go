package apiserver

import (
	"encoding/json"
	"net/http"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
)

// GetDefaultLanguages answers the languages a new library prefers.
func (s *Server) GetDefaultLanguages(w http.ResponseWriter, _ *http.Request) {
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
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "the request body could not be read")
		return
	}
	if err := s.auth.Settings.SetDefaultLanguages(r.Context(), body.Languages); err != nil {
		s.writeTargetError(w, err)
		return
	}
	s.GetDefaultLanguages(w, r)
}
