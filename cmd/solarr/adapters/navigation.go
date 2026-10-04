package adapters

import (
	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/media"
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
