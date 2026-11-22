package mediaserver

import (
	"sort"
	"strings"
)

// PathPair reads a Solarr path prefix as the server's own.
type PathPair struct {
	Solarr string
	Server string
}

// MapPath turns a Solarr path into the server's, by the longest matching prefix; with no pair it passes unchanged.
func MapPath(pairs []PathPair, solarrPath string) (string, bool) {
	return rewrite(pairs, solarrPath, func(pair PathPair) (string, string) { return pair.Solarr, pair.Server })
}

// UnmapPath is MapPath the other way round.
func UnmapPath(pairs []PathPair, serverPath string) (string, bool) {
	return rewrite(pairs, serverPath, func(pair PathPair) (string, string) { return pair.Server, pair.Solarr })
}

func rewrite(pairs []PathPair, path string, sides func(PathPair) (from, to string)) (string, bool) {
	if len(pairs) == 0 {
		return path, false
	}
	ordered := append([]PathPair(nil), pairs...)
	sort.SliceStable(ordered, func(i, j int) bool {
		left, _ := sides(ordered[i])
		right, _ := sides(ordered[j])
		return len(left) > len(right)
	})
	for _, pair := range ordered {
		from, to := sides(pair)
		from = strings.TrimRight(from, "/")
		if path == from || strings.HasPrefix(path, from+"/") {
			return strings.TrimRight(to, "/") + path[len(from):], true
		}
	}
	return path, false
}
