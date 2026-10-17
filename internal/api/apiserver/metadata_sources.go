package apiserver

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/config"
)

func (s *Server) routeGetLibraryMetadataSources(w http.ResponseWriter, r *http.Request) {
	if id, ok := s.categoryIDParam(w, r); ok {
		s.GetLibraryMetadataSources(w, r, id)
	}
}

func (s *Server) routeSetLibraryMetadataSources(w http.ResponseWriter, r *http.Request) {
	if id, ok := s.categoryIDParam(w, r); ok {
		s.SetLibraryMetadataSources(w, r, id)
	}
}

func (s *Server) metadataSourcesView(category settings.Category) gen.LibraryMetadataSources {
	snapshot := s.auth.Settings.Snapshot()
	current, _ := snapshot.Category(category.ID)
	view := gen.LibraryMetadataSources{
		Sources: snapshot.MetadataSourcesFor(current), Custom: boolPtr(len(current.MetadataSources) > 0),
		Default:   settings.DefaultMetadataSources(category.MediaType, category.Kind),
		Available: []gen.MetadataProviderInfo{},
	}
	for _, provider := range settings.MetadataProvidersFor(category.MediaType) {
		view.Available = append(view.Available, gen.MetadataProviderInfo{Key: provider.Key, Label: provider.Label, Carries: provider.Carries, NeedsKey: provider.NeedsKey})
	}
	return view
}

// GetLibraryMetadataSources answers a library's provider order and what its media type could use.
func (s *Server) GetLibraryMetadataSources(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	category, err := s.libraryFor(r, id)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, s.metadataSourcesView(category))
}

// SetLibraryMetadataSources saves a library's provider order; an empty list returns it to the instance's.
func (s *Server) SetLibraryMetadataSources(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	category, err := s.libraryFor(r, id)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	var body struct {
		Sources []string `json:"sources"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "expected sources")
		return
	}
	if err := s.auth.Settings.SetCategoryMetadataSources(r.Context(), id, body.Sources); err != nil {
		var problems config.Problems
		if errors.As(err, &problems) {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "The providers have problems", problems.Report("the providers", "Nothing was changed."))
			return
		}
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, s.metadataSourcesView(category))
}
