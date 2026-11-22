package apiserver

import (
	"net/http"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
)

// ListSettingDefinitions answers every registered setting (ADR-26): key, tier, kind, default, help, including KindEntity surfaces; read-only, task tiers are at GET /schedules.
func (s *Server) ListSettingDefinitions(w http.ResponseWriter, r *http.Request) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.auth.Registry == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "the settings registry is not available")
		return
	}

	defs := s.auth.Registry.All()
	views := make([]gen.SettingDefinition, len(defs))
	for index, def := range defs {
		view := gen.SettingDefinition{
			Key: def.Key, Name: def.Name,
			Tier:    gen.SettingDefinitionTier(def.Tier),
			Kind:    gen.SettingDefinitionKind(def.Kind),
			Default: def.Default,
		}
		if def.HelpText != "" {
			helpText := def.HelpText
			view.HelpText = &helpText
		}
		views[index] = view
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}
