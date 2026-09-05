package inspect

import (
	"sort"
	"strings"
)

// genericContainerWords is deliberately small and media-agnostic -- the kind of
// word a download tool or an OS adds, never an artist or an album across any
// media type. A caller with real linguistic knowledge should supply
// Options.SegmentPlausible instead of relying on this list growing (ADR-23:
// this package stays generic, a richer per-media-type judgement is an
// extension point, not a bigger hardcoded list here).
var genericContainerWords = map[string]bool{
	"downloads": true, "download": true, "complete": true, "incomplete": true,
	"unsorted": true, "misc": true, "temp": true, "tmp": true, "new": true,
}

// containerSegments finds every path segment that carries no entity evidence:
// one identical across every unit beneath it (the "/downloads/misc must never
// become an artist" rule -- a segment every sibling shares is boilerplate, not
// a name), or one a caller or the generic word list scores as implausible.
func containerSegments(units []Unit, plausible SegmentPlausibility) []string {
	countAtDepth := map[int]map[string]int{}
	for _, u := range units {
		for depth, segment := range pathSegments(u.Directory) {
			if countAtDepth[depth] == nil {
				countAtDepth[depth] = map[string]int{}
			}
			countAtDepth[depth][segment]++
		}
	}

	seen := map[string]bool{}
	var containers []string
	add := func(segment string) {
		if !seen[segment] {
			seen[segment] = true
			containers = append(containers, segment)
		}
	}

	for _, counts := range countAtDepth {
		for segment, count := range counts {
			if len(units) > 1 && count == len(units) {
				add(segment)
				continue
			}
			if plausible != nil {
				if confidence, ok := plausible(segment); ok && confidence < 0.2 {
					add(segment)
					continue
				}
			}
			if genericContainerWords[strings.ToLower(segment)] {
				add(segment)
			}
		}
	}
	sort.Strings(containers)
	return containers
}

func pathSegments(directory string) []string {
	trimmed := strings.Trim(directory, "/")
	if trimmed == "" || trimmed == "." {
		return nil
	}
	return strings.Split(trimmed, "/")
}
