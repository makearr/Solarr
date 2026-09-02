package settings

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/makearr/MediaNexus/internal/config"
)

// MediaTypes are the eight the product plans for (architecture Part I).
var MediaTypes = []string{"music", "tv", "movie", "audiobook", "book", "adult", "comic", "rom"}

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
