package settings

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/makearr/MediaNexus/internal/config"
	"github.com/makearr/MediaNexus/internal/score"
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

// validateQualityProfile checks a quality profile before it is written (ADR-44):
// a name, and rules/upgrade JSON that actually decodes as ADR-40's shape --
// internal/score.Parse/ParseUpgrade own that shape, so a typo like `"compare":
// "resolution"` (a string where the schema wants a list) is caught here, not only
// a first evaluation. The interpreter itself never rejects an unknown dimension
// or field name -- an unmeasured/unlisted one just contributes no evidence, by
// design -- so there is no stricter semantic check to add beyond decoding cleanly.
func validateQualityProfile(name, rulesJSON, upgradeJSON string) config.Problems {
	var problems config.Problems
	if strings.TrimSpace(name) == "" {
		problems = append(problems, config.Problem{Path: "name", Expectation: "a name"})
	}
	if _, err := score.Parse([]byte(rulesJSON)); err != nil {
		problems = append(problems, config.Problem{
			Path: "rules", Value: rulesJSON, Expectation: fmt.Sprintf("ADR-40's rule shape: %v", err),
		})
	}
	if _, err := score.ParseUpgrade([]byte(upgradeJSON)); err != nil {
		problems = append(problems, config.Problem{
			Path: "upgrade", Value: upgradeJSON, Expectation: fmt.Sprintf("ADR-40's upgrade shape: %v", err),
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
