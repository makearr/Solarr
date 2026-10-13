package settings

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/db/store/gen/settingsdb"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
)

// Lidarr's "Import Extra Files" and its extension list, off by default like Lidarr (2026-09-24).
const (
	KeyImportExtraFiles          = "import.extra_files"
	KeyImportExtraFileExtensions = "import.extra_file_extensions"
)

// DefaultImportExtraFileExtensions are lyrics, cue sheets, rip logs, release notes and subtitles.
var DefaultImportExtraFileExtensions = []string{"lrc", "cue", "log", "nfo", "srt"}

const maxImportExtraFileExtensions = 32

var extraFileExtensionPattern = regexp.MustCompile(`^[a-z0-9]{1,10}$`)

// ImportExtraFiles is whether import brings sidecars along, and which.
type ImportExtraFiles struct {
	Enabled    bool
	Extensions []string
}

// ImportExtraFiles reads the switch and the list; an absent list is the default one.
func (s *Snapshot) ImportExtraFiles() ImportExtraFiles {
	return ImportExtraFiles{
		Enabled:    s.policyBool(KeyImportExtraFiles, false),
		Extensions: s.policyStringListOr(KeyImportExtraFileExtensions, DefaultImportExtraFileExtensions),
	}
}

// NormaliseExtraFileExtensions turns "LRC, .cue" into [lrc cue], dropping blanks and repeats.
func NormaliseExtraFileExtensions(extensions []string) []string {
	normalised := make([]string, 0, len(extensions))
	seen := make(map[string]bool, len(extensions))
	for _, extension := range extensions {
		extension = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(extension), "."))
		if extension == "" || seen[extension] {
			continue
		}
		seen[extension] = true
		normalised = append(normalised, extension)
	}
	return normalised
}

func validateImportExtraFileExtensions(extensions []string) config.Problems {
	if len(extensions) > maxImportExtraFileExtensions {
		return config.Problems{{Path: KeyImportExtraFileExtensions, Value: strconv.Itoa(len(extensions)),
			Expectation: "at most 32 extensions"}}
	}
	var problems config.Problems
	for _, extension := range extensions {
		if !extraFileExtensionPattern.MatchString(extension) {
			problems = append(problems, config.Problem{Path: KeyImportExtraFileExtensions, Value: extension,
				Expectation: "an extension of letters and digits, such as lrc"})
		}
	}
	return problems
}

// SetImportExtraFiles writes both in one transaction (ADR-44: validated on write).
func (c *Cache) SetImportExtraFiles(ctx context.Context, cl clock.Clock, extras ImportExtraFiles) error {
	extensions := NormaliseExtraFileExtensions(extras.Extensions)
	if problems := validateImportExtraFileExtensions(extensions); len(problems) > 0 {
		return problems
	}
	encoded, err := json.Marshal(extensions)
	if err != nil {
		return err
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		for key, value := range map[string]string{
			KeyImportExtraFiles: boolPolicy(extras.Enabled), KeyImportExtraFileExtensions: string(encoded),
		} {
			err := queries.SetInstanceSetting(ctx, settingsdb.SetInstanceSettingParams{
				Key: key, ValueJson: value, UpdatedAt: cl.Now().Unix(),
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
}
