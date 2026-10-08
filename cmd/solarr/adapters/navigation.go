package adapters

import (
	"slices"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/scanning/scan"
)

// MediaTypes answers apiserver's questions about media types from the registry, so apiserver names none.
type MediaTypes struct{ Registry *media.Registry }

// LevelsFor implements apiserver.NavigationProvider.
func (m MediaTypes) LevelsFor(mediaType string) ([]model.Level, bool) {
	module, ok := m.Registry.Module(media.Key(mediaType))
	if !ok {
		return nil, false
	}
	return module.Descriptor().Levels, true
}

// Known implements apiserver.MediaTypeCatalog.
func (m MediaTypes) Known(mediaType string) bool { return m.Registry.Known(media.Key(mediaType)) }

// ProfilingIn answers the media type of a library as a profiling one, when it is.
func (m MediaTypes) ProfilingIn(categories interface {
	Category(id int64) (settings.Category, bool)
}) func(categoryID int64) (media.Profiling, bool) {
	return func(categoryID int64) (media.Profiling, bool) {
		category, ok := categories.Category(categoryID)
		if !ok {
			return nil, false
		}
		return media.Capability[media.Profiling](m.Registry, media.Key(category.MediaType))
	}
}

// RoleKeys lists every credit role a registered media type declares, once each.
func (m MediaTypes) RoleKeys() []string {
	var keys []string
	for _, descriptor := range m.Registry.Descriptors() {
		for _, role := range descriptor.Roles {
			if !slices.Contains(keys, role.Key) {
				keys = append(keys, role.Key)
			}
		}
	}
	return keys
}

// List implements apiserver.MediaTypeCatalog.
func (m MediaTypes) List() []apiserver.MediaTypeInfo {
	descriptors := m.Registry.Descriptors()
	out := make([]apiserver.MediaTypeInfo, 0, len(descriptors))
	for _, descriptor := range descriptors {
		info := apiserver.MediaTypeInfo{
			Key: string(descriptor.Key), Label: descriptor.Label,
			Work:         apiserver.MediaTerm(descriptor.Vocabulary.Work),
			Part:         apiserver.MediaTerm(descriptor.Vocabulary.Part),
			Group:        apiserver.MediaTerm(descriptor.Vocabulary.Group),
			LeadPeople:   apiserver.MediaTerm(descriptor.Vocabulary.LeadPeople),
			Organisation: apiserver.MediaTerm(descriptor.Vocabulary.Organisation),
		}
		for _, option := range descriptor.FollowOptions {
			view := gen.FollowOption{Key: option.Key, Label: option.Label, Many: option.Many, Default: option.Default}
			for _, choice := range option.Choices {
				view.Choices = append(view.Choices, struct {
					Key   string `json:"key"`
					Label string `json:"label"`
				}{Key: choice.Key, Label: choice.Label})
			}
			info.FollowOptions = append(info.FollowOptions, view)
		}
		for _, facet := range descriptor.Facets {
			info.Facets = append(info.Facets, facetView(facet))
		}
		for _, role := range descriptor.Roles {
			info.Roles = append(info.Roles, apiserver.MediaRole{
				Key: role.Key, Label: role.Label, Lead: role.Lead, Followable: role.Followable,
				PartScoped: role.PartScoped, Character: role.Character,
			})
		}
		out = append(out, info)
	}
	return out
}

// facetView maps one facet as the media types answer shows it.
func facetView(facet media.Facet) gen.Facet {
	view := gen.Facet{
		Key: facet.Key, Label: facet.Label, Entity: gen.FacetEntity(facet.Entity), Kind: gen.FacetKind(facet.Kind),
		Column: facet.Column, Sortable: facet.Sortable, Filterable: facet.Filterable,
	}
	if len(facet.Choices) > 0 {
		choices := make([]struct {
			Key   string `json:"key"`
			Label string `json:"label"`
		}, len(facet.Choices))
		for i, choice := range facet.Choices {
			choices[i].Key, choices[i].Label = choice.Key, choice.Label
		}
		view.Choices = &choices
	}
	return view
}

// MediaSweeps answers scan.Sweeps: the sweep of the media type a category holds.
type MediaSweeps struct {
	Registry *media.Registry
	Cache    *settings.Cache
}

// SweepFor implements scan.Sweeps; a type that declares no consumer is not scanned.
func (m MediaSweeps) SweepFor(categoryID int64) (scan.Sweep, bool) {
	category, found := m.Cache.Snapshot().Category(categoryID)
	if !found {
		return scan.Sweep{}, false
	}
	module, ok := media.Capability[media.Scanning](m.Registry, media.Key(category.MediaType))
	if !ok {
		return scan.Sweep{}, false
	}
	sweep := module.ScanSweep()
	return sweep, sweep.Consumer != nil
}
