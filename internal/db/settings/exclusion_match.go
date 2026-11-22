package settings

import "path/filepath"

// MatchExclusion returns the strictest verdict of every enabled pattern for one file name: Refuse beats Ignore, a pattern scoped elsewhere never applies, an unscoped one applies to all (ADR-53); pure so an import gate can call it.
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
