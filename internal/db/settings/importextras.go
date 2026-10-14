package settings

import (
	"database/sql"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/platform/config"
)

const maxImportExtraFileExtensions = 32

var extraFileExtensionPattern = regexp.MustCompile(`^[a-z0-9]{1,10}$`)

// ExtraFiles is a library's "Import Extra Files" (Sonarr's, Lidarr's): whether import brings the files named after a
// track or episode along, and which. A nil field takes its media type's default.
type ExtraFiles struct {
	Import     *bool
	Extensions []string
}

// Resolve answers the switch and the list, the media type's own where the library set none.
func (e ExtraFiles) Resolve(defaultOn bool, defaultExtensions []string) (bool, []string) {
	on, extensions := defaultOn, defaultExtensions
	if e.Import != nil {
		on = *e.Import
	}
	if e.Extensions != nil {
		extensions = e.Extensions
	}
	return on, extensions
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

func validateExtraFileExtensions(extensions []string) config.Problems {
	const path = "extra_file_extensions"
	if len(extensions) > maxImportExtraFileExtensions {
		return config.Problems{{Path: path, Value: strconv.Itoa(len(extensions)), Expectation: "at most 32 extensions"}}
	}
	var problems config.Problems
	for _, extension := range extensions {
		if !extraFileExtensionPattern.MatchString(extension) {
			problems = append(problems, config.Problem{Path: path, Value: extension,
				Expectation: "an extension of letters and digits, such as srt"})
		}
	}
	return problems
}

func encodeExtraFiles(e ExtraFiles) (onColumn sql.NullInt64, extensionsColumn sql.NullString, err error) {
	if e.Import != nil {
		onColumn = sql.NullInt64{Int64: boolToInt(*e.Import), Valid: true}
	}
	if e.Extensions != nil {
		encoded, err := json.Marshal(e.Extensions)
		if err != nil {
			return onColumn, extensionsColumn, err
		}
		extensionsColumn = sql.NullString{String: string(encoded), Valid: true}
	}
	return onColumn, extensionsColumn, nil
}

func decodeExtraFiles(onColumn sql.NullInt64, extensionsColumn sql.NullString) ExtraFiles {
	var extras ExtraFiles
	if onColumn.Valid {
		on := onColumn.Int64 == 1
		extras.Import = &on
	}
	if extensionsColumn.Valid {
		var extensions []string
		if json.Unmarshal([]byte(extensionsColumn.String), &extensions) == nil {
			extras.Extensions = NormaliseExtraFileExtensions(extensions)
		}
	}
	return extras
}
