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
	agg := newContainerAggregator()
	for _, u := range units {
		agg.Add(u)
	}
	return agg.Result(plausible)
}

// containerAggregator is containerSegments' own counting step, made
// incremental: a Walk cannot hold every Unit in memory at once (that is the
// whole point of streaming), but the counts this needs -- how many units share
// each path segment, and how many units there were in total -- are a handful of
// integers no matter how large the session is. Add is called once per emitted
// unit; Result runs the same decision containerSegments always has, once, at
// the end.
type containerAggregator struct {
	countAtDepth map[int]map[string]int
	totalUnits   int
}

func newContainerAggregator() *containerAggregator {
	return &containerAggregator{countAtDepth: map[int]map[string]int{}}
}

// Add records one more unit's path segments. Order does not matter -- every
// caller of Result needs is a count, not a sequence.
func (a *containerAggregator) Add(u Unit) {
	a.totalUnits++
	for depth, segment := range pathSegments(u.Directory) {
		if a.countAtDepth[depth] == nil {
			a.countAtDepth[depth] = map[string]int{}
		}
		a.countAtDepth[depth][segment]++
	}
}

// Result decides which counted segments are containers, exactly
// containerSegments' own rule.
func (a *containerAggregator) Result(plausible SegmentPlausibility) []string {
	seen := map[string]bool{}
	var containers []string
	add := func(segment string) {
		if !seen[segment] {
			seen[segment] = true
			containers = append(containers, segment)
		}
	}

	for _, counts := range a.countAtDepth {
		for segment, count := range counts {
			if a.totalUnits > 1 && count == a.totalUnits {
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
