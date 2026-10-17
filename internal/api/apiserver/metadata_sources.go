package apiserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
	"github.com/makearr/Solarr/internal/platform/secrets"
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
	defaults := settings.DefaultMetadataSources(category.MediaType, category.Kind)
	view := gen.LibraryMetadataSources{
		Sources: snapshot.MetadataSourcesFor(current), Custom: boolPtr(len(current.MetadataSources) > 0 && !slices.Equal(current.MetadataSources, defaults)),
		Default:   defaults,
		Available: []gen.MetadataProviderInfo{},
	}
	for _, provider := range settings.MetadataProvidersFor(category.MediaType) {
		view.Available = append(view.Available, providerInfo(snapshot, provider))
	}
	return view
}

func providerInfo(snapshot *settings.Snapshot, provider settings.MetadataProvider) gen.MetadataProviderInfo {
	return gen.MetadataProviderInfo{
		Key: provider.Key, Label: provider.Label, Carries: provider.Carries, NeedsKey: provider.NeedsKey && !keyStored(snapshot, provider.Key),
		Enabled: snapshot.ProviderEnabled(provider.Key), CanDisable: provider.Key != settings.SourceMusicBrainz,
		KeyStored:  snapshot.ProviderKeyStored(provider.Key),
		MediaTypes: orEmpty(provider.MediaTypes), AlsoFor: orEmpty(provider.AlsoFor),
	}
}

// keyStored says the owner's key for a provider that needs one is in place.
func keyStored(snapshot *settings.Snapshot, key string) bool { return snapshot.ProviderKeyStored(key) }

// SetMetadataProviderKey stores or clears the owner's key for a provider.
func (s *Server) SetMetadataProviderKey(w http.ResponseWriter, r *http.Request, key string) {
	setting, takesKey := settings.ProviderKeySetting(key)
	if !takesKey {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "that provider takes no key")
		return
	}
	if err := s.auth.Registry.LookupAll(setting); err != nil {
		writeError(w, s.logger, err)
		return
	}
	s.setProviderKey(w, r, setting, func(ctx context.Context, cl clock.Clock, secret secrets.Secret) error {
		return s.auth.Settings.SetProviderKey(ctx, cl, key, secret)
	})
}

func orEmpty(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// ListMetadataProviders answers every provider, what it serves and whether it is on.
func (s *Server) ListMetadataProviders(w http.ResponseWriter, r *http.Request) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	snapshot := s.auth.Settings.Snapshot()
	providers := make([]gen.MetadataProviderInfo, 0, len(settings.MetadataProviders))
	for _, provider := range settings.MetadataProviders {
		providers = append(providers, providerInfo(snapshot, provider))
	}
	writeJSON(w, s.logger, http.StatusOK, struct {
		Providers []gen.MetadataProviderInfo `json:"providers"`
	}{providers})
}

// SetMetadataProviderEnabled switches a provider on or off for every library.
func (s *Server) SetMetadataProviderEnabled(w http.ResponseWriter, r *http.Request, key string) {
	if !s.requireAdministrator(w, r) {
		return
	}
	var body gen.SetMetadataProviderEnabledJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "expected enabled")
		return
	}
	if err := s.auth.Registry.LookupAll(settings.KeyMetadataDisabledProviders, settings.KeyMetadataWikidata, settings.KeyMetadataTheAudioDB, settings.KeyMetadataAcoustID); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if err := s.auth.Settings.SetProviderEnabled(r.Context(), s.wall, key, body.Enabled); err != nil {
		var problems config.Problems
		if errors.As(err, &problems) {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", problems.Error())
			return
		}
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
