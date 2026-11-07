package naming

import (
	"errors"
	"fmt"

	"github.com/makearr/Solarr/internal/db/settings"
)

// Resolved is a library's naming as the importer uses it: the templates by slot, the render options and whether files
// are renamed at all.
type Resolved struct {
	Formats     map[string]string
	Options     Options
	RenameFiles bool
}

// Resolve reads a library's stored profile; nil is the scheme's default preset under defaults.
func (s Scheme) Resolve(profile *settings.NamingProfile, defaults Options) Resolved {
	if profile == nil {
		return Resolved{Formats: s.Default().Formats, Options: defaults, RenameFiles: true}
	}
	return Resolved{
		Formats:     profile.Formats,
		Options:     Options{ReplaceIllegalCharacters: profile.ReplaceIllegalCharacters, ColonReplacement: ColonReplacement(profile.ColonReplacement)},
		RenameFiles: profile.RenameFiles,
	}
}

// DefaultProfile is Resolve(nil, defaults) in stored form: what a never-saved library uses.
func (s Scheme) DefaultProfile(defaults Options) settings.NamingProfile {
	return settings.NamingProfile{
		RenameFiles: true, ReplaceIllegalCharacters: defaults.ReplaceIllegalCharacters,
		ColonReplacement: string(defaults.ColonReplacement), Formats: s.Default().Formats,
	}
}

// ValidateProfile is settings.NamingValidator for the scheme.
func (s Scheme) ValidateProfile(profile settings.NamingProfile) error {
	problems := []error{s.Validate(profile.Formats)}
	if !ColonReplacement(profile.ColonReplacement).Valid() {
		problems = append(problems, fmt.Errorf("naming: unknown colon replacement %q", profile.ColonReplacement))
	}
	return errors.Join(problems...)
}
