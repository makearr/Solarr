package naming

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Scheme is everything a media type declares about naming (ADR-23): the templates it names
// files with, the tokens they may use, and named presets. Core knows none of the vocabulary.
type Scheme struct {
	Slots  []Slot
	Tokens []Token
	// Presets are offered by name; the first is the media type's default.
	Presets []Preset
}

// Slot is one template a media type names files with, e.g. music's "Standard Track Format".
type Slot struct {
	Key   string
	Label string
	Help  string
}

// Token is one {token} explained the way Lidarr's token builder does: what it is, an example.
type Token struct {
	// Spelling is what a person types, "{Artist Name}" or "{track:00}".
	Spelling string
	Group    string
	Example  string
	Help     string
}

// Preset is a named set of templates, one per slot.
type Preset struct {
	Name    string
	Formats map[string]string
}

// Default is the first preset.
func (s Scheme) Default() Preset {
	return s.Presets[0]
}

// Known is every token name the scheme declares, for ValidateTokens.
func (s Scheme) Known() map[string]bool {
	known := make(map[string]bool, len(s.Tokens))
	for _, token := range s.Tokens {
		name := strings.Trim(token.Spelling, "{}")
		name, _, _ = strings.Cut(name, ":")
		known[name] = true
	}
	return known
}

// Validate refuses formats with a missing or unknown slot, a template that does not compile,
// or a token the scheme does not declare -- every problem at once (ADR-21).
func (s Scheme) Validate(formats map[string]string) error {
	var problems []error
	declared := make(map[string]bool, len(s.Slots))
	known := s.Known()
	for _, slot := range s.Slots {
		declared[slot.Key] = true
		template := strings.TrimSpace(formats[slot.Key])
		if template == "" {
			problems = append(problems, fmt.Errorf("%s is empty", slot.Label))
			continue
		}
		segments, err := Compile(template)
		if err == nil {
			err = ValidateTokens(segments, known)
		}
		if err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", slot.Label, err))
		}
	}
	for _, key := range slices.Sorted(maps.Keys(formats)) {
		if !declared[key] {
			problems = append(problems, fmt.Errorf("naming: unknown format %q", key))
		}
	}
	return errors.Join(problems...)
}
