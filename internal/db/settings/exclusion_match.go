package settings

import "path/filepath"

// MatchExclusion evaluates every enabled pattern against one file name,
// returning the strictest verdict that applies. ExclusionRefuse always wins
// over ExclusionIgnore when both match -- a release carrying an executable
// is not a release with one odd file in it, so the decisive action never
// loses to the merely-informative one. A pattern scoped to a different
// media type or category never applies; one scoped to neither applies to
// everything (ADR-53 part 1). Pure and I/O-free, so a future import gate
// (todo 4.15) calls it with whatever Snapshot().ExclusionPatterns() held at
// the time, the same way internal/identify/score is handed a profile rather than
// reaching into settings.db itself.
func MatchExclusion(patterns []ExclusionPattern, name, mediaType string, categoryID *int64) (action string, matched ExclusionPattern, ok bool) {
	var ignoreMatch ExclusionPattern
	haveIgnore := false

	for _, pattern := range patterns {
		if !pattern.Enabled {
			continue
		}
		if pattern.MediaType != "" && pattern.MediaType != mediaType {
			continue
		}
		if pattern.CategoryID != nil && (categoryID == nil || *pattern.CategoryID != *categoryID) {
			continue
		}
		hit, err := filepath.Match(pattern.Pattern, name)
		if err != nil || !hit {
			continue
		}

		if pattern.Action == ExclusionRefuse {
			return ExclusionRefuse, pattern, true
		}
		if !haveIgnore {
			ignoreMatch = pattern
			haveIgnore = true
		}
	}

	if haveIgnore {
		return ExclusionIgnore, ignoreMatch, true
	}
	return "", ExclusionPattern{}, false
}
