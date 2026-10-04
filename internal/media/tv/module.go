package tv

import (
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/moving/naming"
)

// Module is TV as the media registry sees it.
type Module struct{}

// Descriptor implements media.Module.
func (Module) Descriptor() media.Descriptor {
	return media.Descriptor{
		Key:   media.Key(MediaType),
		Label: "TV",
		Vocabulary: media.Vocabulary{
			Work:         media.Term{One: "Series", Many: "Series"},
			Part:         media.Term{One: "Episode", Many: "Episodes"},
			Group:        media.Term{One: "Season", Many: "Seasons"},
			LeadPeople:   media.Term{One: "Cast member", Many: "Cast"},
			Organisation: media.Term{One: "Network", Many: "Networks"},
		},
		Levels: Navigation{}.Levels(),
		Roles: []media.Role{
			{Key: "actor", Label: "Cast", Followable: true, Character: true},
			{Key: "creator", Label: "Creator", Followable: true},
			{Key: "showrunner", Label: "Showrunner", Followable: true},
			{Key: "executive_producer", Label: "Executive producer", Followable: true},
			{Key: "producer", Label: "Producer", Followable: true},
			{Key: "composer", Label: "Composer", Followable: true},
			{Key: "director", Label: "Director", Followable: true, PartScoped: true},
			{Key: "writer", Label: "Writer", Followable: true, PartScoped: true},
			{Key: "guest_star", Label: "Guest star", PartScoped: true, Character: true},
		},
	}
}

// NamingScheme implements media.Naming.
func (Module) NamingScheme() naming.Scheme { return NamingScheme }

// DefaultNamingProfile implements media.Naming.
func (Module) DefaultNamingProfile() settings.NamingProfile { return DefaultNamingProfile() }

// ValidateNaming implements media.Naming.
func (Module) ValidateNaming(profile settings.NamingProfile) error { return ValidateNaming(profile) }

// NamingExamples implements media.Naming.
func (Module) NamingExamples(profile settings.NamingProfile) []media.NamingExample {
	var out []media.NamingExample
	for _, example := range NamingExamples(NamingFor(&profile)) {
		out = append(out, media.NamingExample{Label: example.Label, Path: example.Path})
	}
	return out
}
