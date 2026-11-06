package settings

import (
	"context"
	"fmt"
	"strings"

	"github.com/makearr/Solarr/internal/db/store/gen/settingsdb"
	"github.com/makearr/Solarr/internal/platform/config"
)

// DefaultLanguages are the audio languages a library prefers when it is made, best first; asked in the setup wizard.
func (s *Snapshot) DefaultLanguages() []string { return s.defaultLanguages }

// SetDefaultLanguages replaces the defaults; every code is two or three letters.
func (c *Cache) SetDefaultLanguages(ctx context.Context, codes []string) error {
	var problems config.Problems
	seen := map[string]bool{}
	var clean []string
	for _, code := range codes {
		code = strings.ToLower(strings.TrimSpace(code))
		if len(code) < 2 || len(code) > 3 {
			problems = append(problems, config.Problem{Path: "languages", Value: code, Expectation: "language codes such as en or de"})
			continue
		}
		if !seen[code] {
			seen[code] = true
			clean = append(clean, code)
		}
	}
	if len(problems) > 0 {
		return problems
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		if err := queries.ClearDefaultLanguages(ctx); err != nil {
			return err
		}
		for order, code := range clean {
			if err := queries.AddDefaultLanguage(ctx, settingsdb.AddDefaultLanguageParams{Code: code, Ord: int64(order)}); err != nil {
				return fmt.Errorf("add default language %q: %w", code, err)
			}
		}
		return nil
	})
}

// LanguageProfileFor is the language profile a library gets: the defaults as its audio languages, and for an anime library
// Japanese first, since the originals are Japanese, with the defaults as its subtitles. With no defaults it is English.
func LanguageProfileFor(defaults []string, anime bool) LanguageProfile {
	if len(defaults) == 0 {
		defaults = []string{"en"}
	}
	list := strings.Join(defaults, ", ")
	if !anime {
		return LanguageProfile{Name: "Prefer " + list, AudioPreferred: defaults}
	}
	audio := []string{"ja"}
	for _, code := range defaults {
		if code != "ja" {
			audio = append(audio, code)
		}
	}
	return LanguageProfile{Name: "Japanese, then " + list, AudioPreferred: audio, SubtitlePreferred: defaults}
}
