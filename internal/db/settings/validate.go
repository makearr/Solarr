package settings

import (
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/identify/score"
	"github.com/makearr/Solarr/internal/platform/config"
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

func validateMonitorDiscovered(policy string) config.Problems {
	switch policy {
	case MonitorDiscoveredMonitored, MonitorDiscoveredUnmonitored, MonitorDiscoveredNone:
		return nil
	default:
		return config.Problems{{Path: "monitor_discovered", Value: policy,
			Expectation: "one of monitored, unmonitored, none"}}
	}
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

// validateRootFolderPolicy checks a root folder's user-set placement policy
// before it is written (ADR-44) -- readOnly and allowRename are plain bools,
// nothing to validate about either.
func validateRootFolderPolicy(minFreeBytes int64) config.Problems {
	if minFreeBytes < 0 {
		return config.Problems{{
			Path: "min_free_bytes", Value: strconv.FormatInt(minFreeBytes, 10),
			Expectation: "zero or a positive number of bytes",
		}}
	}
	return nil
}

// validateImportAutoCommitConfidence checks ADR-48's commit line before it is
// written (ADR-44) -- architecture.md's own table explicitly welcomes "a raw
// number ... for anyone who wants one" beyond the five named levels, so this only
// bounds it to a real confidence, not to one of the five.
func validateImportAutoCommitConfidence(confidence float64) config.Problems {
	if confidence < 0 || confidence > 1 {
		return config.Problems{{
			Path: "import.auto_commit_confidence", Value: strconv.FormatFloat(confidence, 'f', -1, 64),
			Expectation: "a confidence between 0 and 1",
		}}
	}
	return nil
}

// validateImportTransferLadder checks the order before it is written (ADR-44).
// An empty ladder is refused rather than silently meaning the default: a user
// who cleared every method asked for something impossible, and answering that
// with a hidden fallback is how a setting stops meaning what it says.
func validateImportTransferLadder(ladder []string) config.Problems {
	var problems config.Problems
	if len(ladder) == 0 {
		problems = append(problems, config.Problem{
			Path: KeyImportTransferLadder, Expectation: "at least one transfer method",
		})
	}
	seen := make(map[string]bool, len(ladder))
	for _, method := range ladder {
		if !ValidTransferMethod(method) {
			problems = append(problems, config.Problem{
				Path: KeyImportTransferLadder, Value: method,
				Expectation: "one of reflink, hardlink, move, copy, symlink",
			})
			continue
		}
		if seen[method] {
			problems = append(problems, config.Problem{
				Path: KeyImportTransferLadder, Value: method,
				Expectation: "each transfer method named at most once",
			})
		}
		seen[method] = true
	}
	return problems
}

// validateQualityProfile checks a quality profile before it is written (ADR-44):
// a name, and rules/upgrade JSON that actually decodes as ADR-40's shape --
// internal/identify/score.Parse/ParseUpgrade own that shape, so a typo like `"compare":
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

// validateExclusionPattern checks an unwanted-file pattern before it is
// written (ADR-53 part 1). categoryExists is a lookup rather than a direct
// Snapshot dependency, so this stays a pure function of its inputs like
// every other validator here.
func validateExclusionPattern(
	pattern, action, mediaType string, categoryID *int64, categoryExists func(int64) bool,
) config.Problems {
	var problems config.Problems

	if strings.TrimSpace(pattern) == "" {
		problems = append(problems, config.Problem{Path: "pattern", Expectation: "a glob pattern"})
	} else if _, err := filepath.Match(pattern, "probe"); err != nil {
		problems = append(problems, config.Problem{
			Path: "pattern", Value: pattern, Expectation: fmt.Sprintf("a valid glob: %v", err),
		})
	}

	if action != ExclusionIgnore && action != ExclusionRefuse {
		problems = append(problems, config.Problem{
			Path: "action", Value: action,
			Expectation: fmt.Sprintf("one of %s, %s", ExclusionIgnore, ExclusionRefuse),
		})
	}

	if mediaType != "" && !slices.Contains(MediaTypes, mediaType) {
		problems = append(problems, config.Problem{
			Path: "media_type", Value: mediaType,
			Expectation: "empty (every media type) or one of " + strings.Join(MediaTypes, ", "),
		})
	}

	if categoryID != nil && !categoryExists(*categoryID) {
		problems = append(problems, config.Problem{
			Path: "category_id", Value: strconv.FormatInt(*categoryID, 10),
			Expectation: "an existing category",
		})
	}

	return problems
}
