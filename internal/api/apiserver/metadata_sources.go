package apiserver

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
	"github.com/makearr/Solarr/internal/platform/secrets"
)

func (s *Server) metadataSourcesView(category settings.Category) gen.LibraryMetadataSources {
	snapshot := s.auth.Settings.Snapshot()
	current, _ := snapshot.Category(category.ID)
	defaults := snapshot.TypeMetadataSources(category.MediaType, category.Kind)
	view := gen.LibraryMetadataSources{
		Sources: snapshot.MetadataSourcesFor(current), Custom: boolPtr(len(current.MetadataSources) > 0 && !slices.Equal(current.MetadataSources, defaults)),
		Default: defaults, IncludeBootlegOnly: boolPtr(current.IncludeBootlegOnly),
		Available: []gen.MetadataProviderInfo{},
	}
	for _, provider := range settings.MetadataProvidersFor(category.MediaType) {
		if (provider.Provisional && !snapshot.LocalSourceOffered(category.ID, provider.Key)) || !provider.ServesKind(category.Kind) || s.hidden("metadata", provider.Key) {
			continue
		}
		view.Available = append(view.Available, s.providerInfo(snapshot, provider))
	}
	return view
}

// The states a metadata provider can be in, for Settings -> Metadata.
const (
	ProviderAnswering   = "answering"
	ProviderFailing     = "failing"
	ProviderKeyRejected = "key_rejected"
	ProviderPaused      = "paused"
)

// MetadataProviderStatus is how a provider stands; Until is when it is asked again, zero while it answers.
type MetadataProviderStatus struct {
	State string
	Until time.Time
}

func (s *Server) providerInfo(snapshot *settings.Snapshot, provider settings.MetadataProvider) gen.MetadataProviderInfo {
	pace, _ := snapshot.ProviderPace(provider.Key)
	ceiling := provider.MaxPerMinute
	if ceiling == 0 {
		ceiling = settings.MaxProviderPerMinute
	}
	status := MetadataProviderStatus{State: ProviderAnswering}
	if s.providerStatus != nil {
		status = s.providerStatus(provider.Key)
	}
	var until *time.Time
	if !status.Until.IsZero() {
		until = &status.Until
	}
	return gen.MetadataProviderInfo{
		Status: gen.MetadataProviderInfoStatus(status.State), StatusUntil: until,
		Key: provider.Key, Label: provider.Label, Carries: provider.Carries, NeedsKey: provider.NeedsKey && !keyStored(snapshot, provider.Key),
		Enabled: snapshot.ProviderEnabled(provider.Key), CanDisable: snapshot.ProviderCanBeSwitchedOff(provider.Key),
		KeyStored:  snapshot.ProviderKeyStored(provider.Key),
		MediaTypes: orEmpty(provider.MediaTypes), AlsoFor: orEmpty(provider.AlsoFor),
		Provisional: boolPtr(provider.Provisional), Custom: boolPtr(provider.Custom),
		PerMinute: pace, DefaultPerMinute: provider.PerMinute, MaxPerMinute: ceiling, Published: provider.Published,
	}
}

// SetMetadataProviderPace sets a provider's pace for every library; the limiter reads it at the next request.
func (s *Server) SetMetadataProviderPace(w http.ResponseWriter, r *http.Request, key string) {
	if !s.requireAdministrator(w, r) {
		return
	}
	var body gen.SetMetadataProviderPaceJSONRequestBody
	if !s.decodeBody(w, r, maxSetupBody, &body, "expected per_minute") {
		return
	}
	if err := s.auth.Registry.LookupAll(settings.KeyMetadataProviderPaces); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if err := s.auth.Settings.SetProviderPace(r.Context(), s.wall, key, body.PerMinute); err != nil {
		var problems config.Problems
		if errors.As(err, &problems) {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", problems.Report("the provider", "Nothing was changed."))
			return
		}
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
		if provider.Provisional && !snapshot.LocalSourceInUse(provider.Key) {
			continue
		}
		providers = append(providers, s.providerInfo(snapshot, provider))
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
	if !s.decodeBody(w, r, maxSetupBody, &body, "expected enabled") {
		return
	}
	if err := s.auth.Registry.LookupAll(settings.KeyMetadataDisabledProviders, settings.KeyMetadataWikidata, settings.KeyMetadataTheAudioDB, settings.KeyMetadataAcoustID); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if err := s.auth.Settings.SetProviderEnabled(r.Context(), s.wall, key, body.Enabled); err != nil {
		var problems config.Problems
		if errors.As(err, &problems) {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", problems.Report("the provider", "Nothing was changed."))
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
		Sources            *[]string `json:"sources"`
		IncludeBootlegOnly *bool     `json:"include_bootleg_only"`
	}
	if !s.decodeBody(w, r, maxSetupBody, &body, "expected sources or include_bootleg_only") {
		return
	}
	if body.Sources != nil {
		err = s.auth.Settings.SetCategoryMetadataSources(r.Context(), id, *body.Sources)
	}
	if err == nil && body.IncludeBootlegOnly != nil {
		err = s.auth.Settings.SetCategoryIncludeBootlegOnly(r.Context(), id, *body.IncludeBootlegOnly)
	}
	if err != nil {
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

// GetLibraryReleaseTypes answers the release types an artist followed in a library brings in.
func (s *Server) GetLibraryReleaseTypes(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	category, err := s.libraryFor(r, id)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, gen.LibraryReleaseTypes{Types: category.MonitorReleaseTypes})
}

// SetLibraryReleaseTypes saves the release types followed artists bring in; at least one.
func (s *Server) SetLibraryReleaseTypes(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if _, err := s.libraryFor(r, id); err != nil {
		writeError(w, s.logger, err)
		return
	}
	var body gen.LibraryReleaseTypes
	if !s.decodeBody(w, r, maxSetupBody, &body, "expected types") {
		return
	}
	if err := s.auth.Settings.SetCategoryMonitorReleaseTypes(r.Context(), id, body.Types); err != nil {
		var problems config.Problems
		if errors.As(err, &problems) {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "The release types have problems", problems.Report("the release types", "Nothing was changed."))
			return
		}
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, gen.LibraryReleaseTypes{Types: body.Types})
}
