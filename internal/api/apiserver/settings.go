package apiserver

import (
	"errors"
	"io"
	"net/http"
	"reflect"

	"github.com/goccy/go-yaml"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/config"
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
	w.Header().Set("Content-Disposition", `attachment; filename="solarr-settings.yaml"`)
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
	// Warn, never refuse (answered 2026-09-24): a key this format does not have is ignored, and
	// said so -- a silent 204 once left a restored library with no root folder.
	var generic any
	_ = yaml.Unmarshal(body, &generic)
	unknown := config.UnknownKeysIn(generic, reflect.TypeOf(settings.Document{}))
	document.NameSettings(settingsKeysIn(generic))

	caller := s.resolvedClientAddress(r)
	if err := s.auth.Settings.Import(r.Context(), s.wall, caller, document); err != nil {
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

	// Same reason AddRootFolder records it inline: a root the document just
	// created has no free space recorded, and placement refuses such a root
	// entirely, so a restored configuration could import nothing at all until
	// the six-hourly sweep happened to run.
	if s.roots != nil {
		for _, root := range s.auth.Settings.Snapshot().RootFolders() {
			if root.FreeBytes != nil {
				continue
			}
			s.recordRootCapacity(r.Context(), root.ID, root.Path, s.roots.Writable(root.Path) == nil)
		}
	}

	s.logger.Info("settings imported",
		"categories", len(document.Categories), "root_folders", len(document.Roots), "unknown_keys", len(unknown))
	result := gen.SettingsImportResult{Warnings: []gen.SettingsImportWarning{}}
	for _, problem := range unknown {
		warning := gen.SettingsImportWarning{Path: problem.Path, Message: "unknown key, ignored"}
		if problem.Suggestion != "" {
			suggestion := problem.Suggestion
			warning.Suggestion = &suggestion
		}
		result.Warnings = append(result.Warnings, warning)
	}
	writeJSON(w, s.logger, http.StatusOK, result)
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

// settingsKeysIn is the keys the document's settings block names, so an absent one keeps its value.
func settingsKeysIn(generic any) []string {
	root, ok := generic.(map[string]any)
	if !ok {
		return nil
	}
	block, ok := root["settings"].(map[string]any)
	if !ok {
		return nil
	}
	keys := make([]string, 0, len(block))
	for key := range block {
		keys = append(keys, key)
	}
	return keys
}
