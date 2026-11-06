package apiserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/config"
)

func (s *Server) libraryTypeDefaultsView(snapshot *settings.Snapshot, mediaType string) gen.LibraryTypeDefaults {
	defaults := snapshot.TypeDefaultsFor(mediaType)
	view := gen.LibraryTypeDefaults{
		MediaType: mediaType, MetadataSources: snapshot.TypeMetadataSources(mediaType, ""), MetadataCustom: len(defaults.MetadataSources) > 0,
		MetadataDefault:   &[]string{},
		MetadataAvailable: []gen.MetadataProviderInfo{}, QualityProfileIds: orEmptyIDs(defaults.QualityProfileIDs), TargetAuto: defaults.TargetAuto,
		TargetId: defaults.TargetID, LanguageProfileId: defaults.LanguageProfileID,
		AvailableProfiles: []gen.QualityProfileRef{}, AvailableTargets: []gen.QualityProfileRef{}, AvailableLanguages: []gen.QualityProfileRef{},
	}
	shipped := settings.DefaultMetadataSources(mediaType, "")
	view.MetadataDefault = &shipped
	for _, provider := range settings.MetadataProvidersFor(mediaType) {
		view.MetadataAvailable = append(view.MetadataAvailable, s.providerInfo(snapshot, provider))
	}
	for _, profile := range snapshot.QualityProfiles() {
		if profile.MediaType == mediaType {
			view.AvailableProfiles = append(view.AvailableProfiles, gen.QualityProfileRef{Id: profile.ID, Name: profile.Name})
		}
	}
	for _, target := range snapshot.QualityTargets() {
		if target.MediaType == settings.TargetFamily(mediaType) {
			view.AvailableTargets = append(view.AvailableTargets, gen.QualityProfileRef{Id: target.ID, Name: targetDisplayName(target)})
		}
	}
	for _, profile := range snapshot.LanguageProfiles() {
		view.AvailableLanguages = append(view.AvailableLanguages, gen.QualityProfileRef{Id: profile.ID, Name: profile.Name})
	}
	return view
}

func orEmptyIDs(ids []int64) []int64 {
	if ids == nil {
		return []int64{}
	}
	return ids
}

// ListLibraryTypeDefaults answers, per media type, what its libraries start with.
func (s *Server) ListLibraryTypeDefaults(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	snapshot := s.auth.Settings.Snapshot()
	types := []gen.LibraryTypeDefaults{}
	for _, mediaType := range settings.MediaTypes {
		if _, known := s.mediaTypeInfo(mediaType); known {
			types = append(types, s.libraryTypeDefaultsView(snapshot, mediaType))
		}
	}
	writeJSON(w, s.logger, http.StatusOK, struct {
		Types []gen.LibraryTypeDefaults `json:"types"`
	}{types})
}

func (s *Server) routeSetLibraryTypeDefaults(w http.ResponseWriter, r *http.Request) {
	s.SetLibraryTypeDefaults(w, r, chi.URLParam(r, "mediaType"))
}

// SetLibraryTypeDefaults changes a media type's defaults; a field left out stays as it is.
func (s *Server) SetLibraryTypeDefaults(w http.ResponseWriter, r *http.Request, mediaType string) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if _, known := s.mediaTypeInfo(mediaType); !known {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such media type")
		return
	}
	var body gen.LibraryTypeDefaultsWrite
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "the request body could not be read")
		return
	}
	if err := s.auth.Registry.LookupAll(settings.KeyLibraryTypeDefaults); err != nil {
		writeError(w, s.logger, err)
		return
	}
	defaults := s.auth.Settings.Snapshot().TypeDefaultsFor(mediaType)
	if body.MetadataSources != nil {
		defaults.MetadataSources = slices.Clone(*body.MetadataSources)
	}
	if body.QualityProfileIds != nil {
		defaults.QualityProfileIDs = slices.Clone(*body.QualityProfileIds)
	}
	if body.TargetId != nil {
		defaults.TargetID = nil
		if *body.TargetId != 0 {
			defaults.TargetID = body.TargetId
		}
		defaults.TargetAuto = false
	}
	if body.TargetAuto != nil {
		defaults.TargetAuto = *body.TargetAuto
		if defaults.TargetAuto {
			defaults.TargetID = nil
		}
	}
	if body.LanguageProfileId != nil {
		defaults.LanguageProfileID = nil
		if *body.LanguageProfileId != 0 {
			defaults.LanguageProfileID = body.LanguageProfileId
		}
	}
	if err := s.auth.Settings.SetTypeDefaults(r.Context(), s.wall, mediaType, defaults); err != nil {
		var problems config.Problems
		if errors.As(err, &problems) {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", problems.Report("the type's defaults", "Nothing was changed."))
			return
		}
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, s.libraryTypeDefaultsView(s.auth.Settings.Snapshot(), mediaType))
}
