package apiserver

import (
	"errors"
	"io"
	"net/http"

	"github.com/goccy/go-yaml"

	"github.com/makearr/MediaNexus/internal/authz"
	"github.com/makearr/MediaNexus/internal/config"
	"github.com/makearr/MediaNexus/internal/settings"
)

// maxImportBody bounds a configuration document. Generous for something a
// person edits, small enough that it cannot be used to make the server read
// arbitrarily much.
const maxImportBody = 1 << 20

const yamlContentType = "application/yaml"

// ExportSettings renders the configuration as YAML (F9).
//
// Administrator only. The document carries no credentials, but it does describe
// the whole shape of someone's library, which is not a read-only user's to take.
func (s *Server) ExportSettings(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}

	encoded, err := yaml.Marshal(s.auth.Settings.Export())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}

	w.Header().Set("Content-Type", yamlContentType)
	// Named so a browser saves something recognisable rather than "export".
	w.Header().Set("Content-Disposition", `attachment; filename="medianexus-settings.yaml"`)
	if _, err := w.Write(encoded); err != nil {
		s.logger.Warn("could not write the settings export", "error", err)
	}
}

// ImportSettings applies a document, entirely or not at all.
func (s *Server) ImportSettings(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxImportBody))
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "the document could not be read, or is too large")
		return
	}

	var document settings.Document
	if err := yaml.Unmarshal(body, &document); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"That is not a configuration document", err.Error())
		return
	}

	if err := s.auth.Settings.Import(r.Context(), document); err != nil {
		// Validation failures carry every problem, and the whole list is what
		// makes the response useful -- reporting only the first turns a
		// five-mistake file into five uploads.
		var problems config.Problems
		if errors.As(err, &problems) {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"The document has problems",
				problems.Report("the document", "Nothing was changed."))
			return
		}
		writeError(w, s.logger, err)
		return
	}

	s.logger.Info("settings imported",
		"categories", len(document.Categories), "root_folders", len(document.Roots))
	w.WriteHeader(http.StatusNoContent)
}

// requireAdministrator answers the request itself when the caller is not one,
// so a handler cannot forget to stop after checking.
func (s *Server) requireAdministrator(w http.ResponseWriter, r *http.Request) bool {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return false
	}
	if access.Role() != settings.RoleAdmin {
		writeProblem(w, s.logger, http.StatusForbidden, TypeForbidden,
			"Forbidden", "only an administrator may read or change configuration")
		return false
	}
	return true
}
