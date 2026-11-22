package inspect

import (
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// discSubfolderRe recognises the disc-subfolder pattern (section 11.9): "CD1", "Disc 2", "Disk-3", "Vol. 4", "DVD1",
// "BD 1", "Digital Media 01", "Side A", each optionally followed by a title ("CD1 - Live", "Disc 2 (Bonus)").
var discSubfolderRe = regexp.MustCompile(`(?i)^(?:cd|dis[ck]|dvd|bd|blu-?ray|vol(?:ume)?|digital media|side)\.?\s*[-_#]?\s*(\d{1,2}|[a-d])(?:\s*[-:(\[].*)?$`)

// DiscNumber is the disc a folder name stands for ("CD2 - Live" is 2, "Side B" is 2), false for any other name.
func DiscNumber(dirName string) (int, bool) {
	found := discSubfolderRe.FindStringSubmatch(dirName)
	if found == nil {
		return 0, false
	}
	if number, err := strconv.Atoi(found[1]); err == nil {
		return number, number > 0
	}
	return int(strings.ToLower(found[1])[0]-'a') + 1, true
}

// leadingTrackNumberRe pulls a track number off the front of a filename --
// "01 - Title.flac", "01_title.mp3", "1.flac" all match.
var leadingTrackNumberRe = regexp.MustCompile(`^0*(\d{1,3})\b`)

func firstPath(files []File) string {
	if len(files) == 0 {
		return ""
	}
	return files[0].RelPath
}

// splitByEvidence decides whether a directory's media files are one unit or several; extra is a signal established earlier (a disc-subfolder merge) that every resulting unit carries.
func splitByEvidence(directory string, files []File, tags TagGrouper, extra *Signal) []Unit {
	// A disc-subfolder merge already explains why track numbering restarts
	// (CD2 starts at 01 same as CD1) -- letting the structural fallback run
	// on top would split the very set that signal just merged back apart.
	structuralFallbackAllowed := extra == nil
	groups, signals, ambiguous := splitFiles(files, tags, structuralFallbackAllowed)

	units := make([]Unit, len(groups))
	for i, g := range groups {
		evidence := Evidence{Ambiguous: ambiguous}
		if extra != nil {
			evidence.Signals = append(evidence.Signals, *extra)
		}
		evidence.Signals = append(evidence.Signals, signals[i]...)
		units[i] = Unit{Directory: directory, Media: g, Evidence: evidence}
	}
	return units
}

// splitFiles is the per-directory decision: tag agreement first, track-number continuity as the only structural fallback, else the whole directory as one group.
func splitFiles(files []File, tags TagGrouper, structuralFallbackAllowed bool) (groups [][]File, signals [][]Signal, ambiguous bool) {
	if tags != nil {
		if g, s, unsure, ok := splitByTags(files, tags); ok {
			return g, s, unsure
		}
	}
	if structuralFallbackAllowed {
		if g, s, ok := splitByTrackContinuity(files); ok {
			return g, s, true
		}
	}
	return [][]File{files}, [][]Signal{{{Kind: SignalDirectoryBoundary}}}, false
}

// splitByTags groups by what the media type's tags say. ok is false when they say nothing, or when the grouper
// does not place every file exactly once, so the caller falls through to the structural signal.
func splitByTags(files []File, tags TagGrouper) (groups [][]File, signals [][]Signal, ambiguous, ok bool) {
	relPaths := make([]string, len(files))
	for i, f := range files {
		relPaths[i] = f.RelPath
	}
	grouped, ok := tags(relPaths)
	if !ok || !placesEachOnce(grouped, len(files)) {
		return nil, nil, false, false
	}
	for g, members := range grouped.Groups {
		group := make([]File, len(members))
		for i, member := range members {
			group[i] = files[member]
		}
		groups = append(groups, group)
		signals = append(signals, []Signal{grouped.Signals[g]})
	}
	return groups, signals, grouped.Ambiguous, true
}

// placesEachOnce reports whether groups hold every one of count files exactly once, with a signal per group.
func placesEachOnce(grouped TagGroups, count int) bool {
	if len(grouped.Signals) != len(grouped.Groups) {
		return false
	}
	seen := make([]bool, count)
	placed := 0
	for _, members := range grouped.Groups {
		for _, member := range members {
			if member < 0 || member >= count || seen[member] {
				return false
			}
			seen[member] = true
			placed++
		}
	}
	return placed == count
}

// splitByTrackContinuity treats a restarting track number as a new group; ok is false with fewer than two parseable numbers or no repeated number (see the duplicate gate), and files are read in mtime order because a filename sort interleaves albums dropped in one flat folder (section 11.9, defeated by a copy that flattens mtimes).
func splitByTrackContinuity(files []File) (groups [][]File, signals [][]Signal, ok bool) {
	type numbered struct {
		file   File
		number int
		has    bool
	}
	ordered := make([]numbered, len(files))
	sorted := append([]File(nil), files...)
	sort.Slice(sorted, func(i, j int) bool {
		if !sorted[i].ModTime.Equal(sorted[j].ModTime) {
			return sorted[i].ModTime.Before(sorted[j].ModTime)
		}
		return sorted[i].RelPath < sorted[j].RelPath
	})
	counts := map[int]int{}
	for i, f := range sorted {
		match := leadingTrackNumberRe.FindStringSubmatch(path.Base(f.RelPath))
		if match == nil {
			ordered[i] = numbered{file: f}
			continue
		}
		n, _ := strconv.Atoi(match[1])
		ordered[i] = numbered{file: f, number: n, has: true}
		counts[n]++
	}

	numberedCount := 0
	duplicate := false
	for _, c := range counts {
		numberedCount += c
		if c > 1 {
			duplicate = true
		}
	}
	if numberedCount < 2 {
		return nil, nil, false
	}
	// A real release's track numbers are unique and mtime is only a weak proxy for track order (segmented downloads finish in any order), so a decrease with no repeated number is mtime noise, not a second release.
	if !duplicate {
		return nil, nil, false
	}

	var current []File
	last := 0
	restarts := 0
	for _, o := range ordered {
		if o.has && o.number < last {
			groups = append(groups, current)
			current = nil
			restarts++
		}
		current = append(current, o.file)
		if o.has {
			last = o.number
		}
	}
	groups = append(groups, current)
	if restarts == 0 {
		return nil, nil, false
	}
	for range groups {
		signals = append(signals, []Signal{{Kind: SignalTrackContinuity}})
	}
	return groups, signals, true
}
