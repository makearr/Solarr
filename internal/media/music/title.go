package music

import (
	"path"
	"strings"
)

// albumTitleFallback names a work when no album tag was readable: the containing
// directory, which is how a Lidarr-managed library already names an album folder.
func albumTitleFallback(relPath string) string {
	dir := path.Dir(relPath)
	if dir == "." || dir == "/" {
		return strings.TrimSuffix(path.Base(relPath), path.Ext(relPath))
	}
	return path.Base(dir)
}

// sortTitle lowercases, collapses whitespace and drops a leading article, so "The
// Genoc" sorts under B. No spec defines this precisely yet; refine when it matters.
func sortTitle(title string) string {
	lowered := strings.ToLower(strings.TrimSpace(title))
	lowered = strings.Join(strings.Fields(lowered), " ")
	for _, article := range []string{"the ", "a ", "an "} {
		if rest, ok := strings.CutPrefix(lowered, article); ok {
			return rest
		}
	}
	return lowered
}
