package apiserver

import (
	"errors"
	"io"
	"net/http"

	"github.com/goccy/go-yaml"

	"github.com/makearr/MediaNexus/internal/api/authz"
	"github.com/makearr/MediaNexus/internal/db/settings"
	"github.com/makearr/MediaNexus/internal/platform/config"
)

// Generous for a hand-edited file, bounded against an unauthenticated read.
const maxImportBody = 1 << 20

const yamlContentType = "application/yaml"

// ExportSettings renders the configuration as YAML (F9). Administrator only:
// it describes the whole shape of a library.
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
	// Named, or a browser saves it as "export".
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
		// The whole list is what makes the response useful.
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

// requireAdministrator answers the request itself when the caller is not one.
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
