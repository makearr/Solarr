// Package watch decides which library directories to watch for changes and when a root is due for a scan, so a
// healthy watcher replaces most directory walks of a network disk.
package watch

import "sort"

// Dir is a directory of a root folder that holds library files; Rel is "" for the root itself.
type Dir struct {
	RootFolderID int64
	Rel          string
	Depth        int
	// Changed is when a file in it last appeared, in Unix seconds; the most recently changed directories are
	// watched first when there are not enough watches for every directory of a level.
	Changed int64
}

// Plan picks the directories to watch within budget: shallow levels before deep ones, so a new artist or series is
// always seen, and within the level where the budget runs out the recently changed first. CoveredDepth is the deepest
// level watched completely (-1 when not even the roots are).
func Plan(dirs []Dir, budget int) (chosen []Dir, coveredDepth int) {
	ordered := append([]Dir(nil), dirs...)
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		switch {
		case a.Depth != b.Depth:
			return a.Depth < b.Depth
		case a.Changed != b.Changed:
			return a.Changed > b.Changed
		case a.RootFolderID != b.RootFolderID:
			return a.RootFolderID < b.RootFolderID
		default:
			return a.Rel < b.Rel
		}
	})
	if budget < len(ordered) {
		ordered = ordered[:max(budget, 0)]
	}
	return ordered, coveredDepthOf(dirs, ordered)
}

func coveredDepthOf(all, chosen []Dir) int {
	total, taken := map[int]int{}, map[int]int{}
	deepest := -1
	for _, dir := range all {
		total[dir.Depth]++
		deepest = max(deepest, dir.Depth)
	}
	for _, dir := range chosen {
		taken[dir.Depth]++
	}
	covered := -1
	for depth := 0; depth <= deepest; depth++ {
		if total[depth] == 0 {
			continue
		}
		if taken[depth] < total[depth] {
			break
		}
		covered = depth
	}
	return covered
}

// RecommendedWatches is how many inotify watches the host should allow for this many directories: the next power of
// two, doubled so Solarr's use stays within half of the limit (user's rule, 2026-11-10).
func RecommendedWatches(directories int) int64 {
	needed := int64(1)
	for needed < int64(directories) {
		needed *= 2
	}
	return needed * 2
}
