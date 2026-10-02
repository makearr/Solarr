package adapters

import (
	"fmt"
	"strings"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/moving/naming"
)

// namingModule is one media type's naming declaration; a new media type adds one entry (ADR-23).
type namingModule struct {
	scheme         naming.Scheme
	defaultProfile func() settings.NamingProfile
	validate       func(settings.NamingProfile) error
	examples       func(settings.NamingProfile) []apiserver.NamingExample
}

var namingModules = map[string]namingModule{
	"music": {
		scheme: music.NamingScheme, defaultProfile: music.DefaultNamingProfile, validate: music.ValidateNaming,
		examples: func(profile settings.NamingProfile) []apiserver.NamingExample {
			var out []apiserver.NamingExample
			for _, example := range music.NamingExamples(music.NamingFor(&profile)) {
				out = append(out, apiserver.NamingExample{Label: example.Label, Path: example.Path})
			}
			return out
		},
	},
	"tv": {
		scheme: tv.NamingScheme, defaultProfile: tv.DefaultNamingProfile, validate: tv.ValidateNaming,
		examples: func(profile settings.NamingProfile) []apiserver.NamingExample {
			var out []apiserver.NamingExample
			for _, example := range tv.NamingExamples(tv.NamingFor(&profile)) {
				out = append(out, apiserver.NamingExample{Label: example.Label, Path: example.Path})
			}
			return out
		},
	},
}

// ValidateNaming is settings.NamingValidator over every media type with a scheme.
func ValidateNaming(mediaType string, profile settings.NamingProfile) error {
	module, ok := namingModules[mediaType]
	if !ok {
		return fmt.Errorf("naming: %s has no naming scheme yet", mediaType)
	}
	return module.validate(profile)
}

// NamingSchemes satisfies apiserver.NamingSchemes over the media types that declare one.
type NamingSchemes struct{}

// Scheme converts a media type's declaration into apiserver's shape.
func (NamingSchemes) Scheme(mediaType string) (apiserver.NamingScheme, bool) {
	module, ok := namingModules[mediaType]
	if !ok {
		return apiserver.NamingScheme{}, false
	}
	view := apiserver.NamingScheme{Default: module.defaultProfile()}
	for _, slot := range module.scheme.Slots {
		view.Slots = append(view.Slots, apiserver.NamingSlot{Key: slot.Key, Label: slot.Label, Help: slot.Help})
	}
	for _, token := range module.scheme.Tokens {
		view.Tokens = append(view.Tokens, apiserver.NamingToken{
			Spelling: token.Spelling, Group: token.Group, Example: token.Example, Help: token.Help,
		})
	}
	for _, preset := range module.scheme.Presets {
		view.Presets = append(view.Presets, apiserver.NamingPreset{Name: preset.Name, Formats: preset.Formats})
	}
	return view, true
}

// Examples renders a media type's samples, or the problems that would refuse the profile on save.
func (NamingSchemes) Examples(mediaType string, profile settings.NamingProfile) ([]apiserver.NamingExample, []string) {
	module, ok := namingModules[mediaType]
	if !ok {
		return nil, []string{mediaType + " has no naming scheme yet"}
	}
	if err := module.validate(profile); err != nil {
		return nil, strings.Split(err.Error(), "\n")
	}
	return module.examples(profile), nil
}
