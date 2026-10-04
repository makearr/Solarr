package adapters

import (
	"fmt"
	"strings"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
)

// NamingValidator is settings.NamingValidator over every registered media type with a scheme.
func NamingValidator(registry *media.Registry) settings.NamingValidator {
	return func(mediaType string, profile settings.NamingProfile) error {
		module, ok := media.Capability[media.Naming](registry, media.Key(mediaType))
		if !ok {
			return fmt.Errorf("naming: %s has no naming scheme yet", mediaType)
		}
		return module.ValidateNaming(profile)
	}
}

// NamingSchemes satisfies apiserver.NamingSchemes over the media types that declare one.
type NamingSchemes struct{ Registry *media.Registry }

// Scheme converts a media type's declaration into apiserver's shape.
func (n NamingSchemes) Scheme(mediaType string) (apiserver.NamingScheme, bool) {
	module, ok := media.Capability[media.Naming](n.Registry, media.Key(mediaType))
	if !ok {
		return apiserver.NamingScheme{}, false
	}
	scheme := module.NamingScheme()
	view := apiserver.NamingScheme{Default: module.DefaultNamingProfile()}
	for _, slot := range scheme.Slots {
		view.Slots = append(view.Slots, apiserver.NamingSlot{Key: slot.Key, Label: slot.Label, Help: slot.Help})
	}
	for _, token := range scheme.Tokens {
		view.Tokens = append(view.Tokens, apiserver.NamingToken{
			Spelling: token.Spelling, Group: token.Group, Example: token.Example, Help: token.Help,
		})
	}
	for _, preset := range scheme.Presets {
		view.Presets = append(view.Presets, apiserver.NamingPreset{Name: preset.Name, Formats: preset.Formats})
	}
	return view, true
}

// Examples renders a media type's samples, or the problems that would refuse the profile on save.
func (n NamingSchemes) Examples(mediaType string, profile settings.NamingProfile) ([]apiserver.NamingExample, []string) {
	module, ok := media.Capability[media.Naming](n.Registry, media.Key(mediaType))
	if !ok {
		return nil, []string{mediaType + " has no naming scheme yet"}
	}
	if err := module.ValidateNaming(profile); err != nil {
		return nil, strings.Split(err.Error(), "\n")
	}
	var out []apiserver.NamingExample
	for _, example := range module.NamingExamples(profile) {
		out = append(out, apiserver.NamingExample{Label: example.Label, Path: example.Path})
	}
	return out, nil
}
