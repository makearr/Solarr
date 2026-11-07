package library

import "strings"

// SortTitle lowercases a title, collapses whitespace and drops a leading article, so "The Example Film" sorts under E.
func SortTitle(title string) string {
	lowered := strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(title))), " ")
	for _, article := range []string{"the ", "a ", "an "} {
		if rest, ok := strings.CutPrefix(lowered, article); ok {
			return rest
		}
	}
	return lowered
}
