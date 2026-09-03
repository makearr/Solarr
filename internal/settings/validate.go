package settings

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/makearr/MediaNexus/internal/config"
)

// MediaTypes are the eleven the product plans for (architecture Part I). Manga,
// magazines, music videos and adult galleries are categories of these, not types.
var MediaTypes = []string{
	"music", "tv", "movie", "audiobook", "book", "adult", "comic", "rom",
	"podcast", "news", "youtube",
}

// maxPriorityRank keeps the composite acquisition priority in a sane range (ADR-36).
const maxPriorityRank = 10_000

// validateCategory checks a category before it is written, not at startup, so a
// bad value is refused where it is entered (ADR-44, section 14.4).
func validateCategory(mediaType, name string, priorityRank int64) config.Problems {
	var problems config.Problems

	if !slices.Contains(MediaTypes, mediaType) {
		problems = append(problems, config.Problem{
			Path:        "media_type",
			Value:       mediaType,
			Expectation: "one of " + strings.Join(MediaTypes, ", "),
		})
	}
	if strings.TrimSpace(name) == "" {
		problems = append(problems, config.Problem{Path: "name", Expectation: "a name"})
	}
	if priorityRank < 0 || priorityRank > maxPriorityRank {
		problems = append(problems, config.Problem{
			Path:        "priority_rank",
			Value:       fmt.Sprint(priorityRank),
			Expectation: fmt.Sprintf("a rank between 0 and %d", maxPriorityRank),
		})
	}
	return problems
}

// validateRootFolder checks a library root before it is written. Whether the
// path is writable is reported elsewhere and never refused.
func validateRootFolder(path string) config.Problems {
	var problems config.Problems

	switch {
	case strings.TrimSpace(path) == "":
		problems = append(problems, config.Problem{Path: "path", Expectation: "a path"})
	case !filepath.IsAbs(path):
		problems = append(problems, config.Problem{
			Path: "path", Value: path, Expectation: "an absolute path",
		})
	}
	return problems
}

// validateQualityProfile checks a quality profile before it is written (ADR-44).
// This layer only guards what settings.db itself must never hold -- a name and
// syntactically valid JSON. The rules' actual shape (gates/tiers/scoring, ADR-40)
// is internal/score's to validate; settings does not import it (this package sits
// below score in the dependency graph), so a structurally wrong-but-valid-JSON
// policy is caught only when internal/score first evaluates it, not on write.
func validateQualityProfile(name, rulesJSON, upgradeJSON string) config.Problems {
	var problems config.Problems
	if strings.TrimSpace(name) == "" {
		problems = append(problems, config.Problem{Path: "name", Expectation: "a name"})
	}
	if !json.Valid([]byte(rulesJSON)) {
		problems = append(problems, config.Problem{
			Path: "rules", Value: rulesJSON, Expectation: "valid JSON",
		})
	}
	if !json.Valid([]byte(upgradeJSON)) {
		problems = append(problems, config.Problem{
			Path: "upgrade", Value: upgradeJSON, Expectation: "valid JSON",
		})
	}
	return problems
}

// validateLanguageProfile checks a language profile before it is written. An
// empty preferred/required list is valid -- section 19.8 gives adult and music
// categories no language policy at all.
func validateLanguageProfile(name string) config.Problems {
	var problems config.Problems
	if strings.TrimSpace(name) == "" {
		problems = append(problems, config.Problem{Path: "name", Expectation: "a name"})
	}
	return problems
}
