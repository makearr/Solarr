package adapters

import (
	"strings"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media/music"
)

// NamingSchemes satisfies apiserver.NamingSchemes; music is the one media type with a scheme.
type NamingSchemes struct{}

// Scheme converts music's declaration into apiserver's shape.
func (NamingSchemes) Scheme(mediaType string) (apiserver.NamingScheme, bool) {
	if mediaType != "music" {
		return apiserver.NamingScheme{}, false
	}
	scheme := music.NamingScheme
	view := apiserver.NamingScheme{Default: music.DefaultNamingProfile()}
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

// Examples renders music's samples, or the problems that would refuse the profile on save.
func (NamingSchemes) Examples(mediaType string, profile settings.NamingProfile) ([]apiserver.NamingExample, []string) {
	if mediaType != "music" {
		return nil, []string{mediaType + " has no naming scheme yet"}
	}
	if err := music.ValidateNaming(profile); err != nil {
		return nil, strings.Split(err.Error(), "\n")
	}
	var examples []apiserver.NamingExample
	for _, example := range music.NamingExamples(music.NamingFor(&profile)) {
		examples = append(examples, apiserver.NamingExample{Label: example.Label, Path: example.Path})
	}
	return examples, nil
}
