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

// group partitions files' media into units. Non-media files are attached to
// whichever unit shares their directory, or dropped if no unit claims that
// directory -- Run's caller sees them via Session, not lost, since a stray
// cover.jpg beside no album is still evidence, not an import failure.
func group(files []File, tags TagGrouper) []Unit {
	mediaByDir := map[string][]File{}
	otherByDir := map[string][]File{}
	for _, f := range files {
		dir := path.Dir(f.RelPath)
		if f.Class == ClassMedia {
			mediaByDir[dir] = append(mediaByDir[dir], f)
		} else {
			otherByDir[dir] = append(otherByDir[dir], f)
		}
	}

	merged, standalone := mergeDiscSubfolders(mediaByDir)

	var units []Unit
	for _, m := range merged {
		units = append(units, splitByEvidence(m.directory, m.files, tags, &Signal{
			Kind: SignalDiscSubfolder, Detail: strings.Join(m.children, ", "),
		})...)
	}
	for _, dir := range standalone {
		units = append(units, splitByEvidence(dir, mediaByDir[dir], tags, nil)...)
	}

	sort.Slice(units, func(i, j int) bool {
		if units[i].Directory != units[j].Directory {
			return units[i].Directory < units[j].Directory
		}
		return firstPath(units[i].Media) < firstPath(units[j].Media)
	})

	attachOther(units, otherByDir)
	return units
}

func firstPath(files []File) string {
	if len(files) == 0 {
		return ""
	}
	return files[0].RelPath
}

type mergedGroup struct {
	directory string
	children  []string
	files     []File
}

// mergeDiscSubfolders folds CD1/CD2-shaped sibling directories into one group
// keyed on their parent, and returns every other media directory untouched.
// A parent qualifies once any of its immediate children matches the pattern --
// requiring all of them would break on a disc set with one oddly-named volume.
func mergeDiscSubfolders(mediaByDir map[string][]File) ([]mergedGroup, []string) {
	byParent := map[string][]string{}
	for dir := range mediaByDir {
		if dir == "." || dir == "" {
			continue
		}
		if discSubfolderRe.MatchString(path.Base(dir)) {
			parent := path.Dir(dir)
			byParent[parent] = append(byParent[parent], dir)
		}
	}

	merged := map[string]bool{}
	var groups []mergedGroup
	for parent, children := range byParent {
		sort.Strings(children)
		var files []File
		for _, child := range children {
			files = append(files, mediaByDir[child]...)
			merged[child] = true
		}
		// The parent's own direct files, if any, join the merge too -- a disc
		// set sometimes carries a bonus-disc track loose beside the CD folders.
		if direct, ok := mediaByDir[parent]; ok {
			files = append(files, direct...)
			merged[parent] = true
		}
		groups = append(groups, mergedGroup{directory: parent, children: children, files: files})
	}

	var standalone []string
	for dir := range mediaByDir {
		if !merged[dir] {
			standalone = append(standalone, dir)
		}
	}
	sort.Strings(standalone)
	return groups, standalone
}

// splitByEvidence decides whether directory's media files are one unit or
// several, and returns the resulting units. extra, when non-nil, is a signal
// already established before this call (a disc-subfolder merge) that every
// resulting unit carries regardless of how the split itself resolves.
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

// splitFiles is the actual per-directory decision: tag agreement first (the
// decisive and high-weight signals), track-number continuity as the only
// purely structural fallback, and the whole directory as one group -- the
// default -- when neither signal has anything to say.
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

// splitByTrackContinuity is the one purely-structural splitter: track numbers
// that restart (drop back down rather than keep climbing) mark a new group.
// ok is false when fewer than two files carry a parseable track number, since
// one data point proves nothing, and also when no number repeats anywhere in
// the set -- see the duplicate gate below.
//
// Files are read in modification-time order, not filename order: two albums
// dropped into one flat folder each keep their own 01, 02, 03... run in the
// order they were written, but a plain filename sort interleaves both albums'
// "01"s before either "02" (section 11.9's own "mtime clustering" signal is
// what makes the "track continuity" signal usable at all here, a copy
// operation that flattens mtimes is the known limit of this heuristic).
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
	// A real release's own track numbers are unique; mtime order is only a
	// weak proxy for track order -- a segmented usenet/torrent download can
	// finish files in any order regardless of track number -- so a "decrease"
	// with no number actually repeated is mtime noise, not a second release.
	// Confirmed live: a real, cleanly-tagged Vutu Doci album (unique 01-13,
	// scrambled mtimes) split 6 ways on this alone before the gate.
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

// attachOther gives each unit the non-media files sitting in its own directory.
// A merged unit collects its children's non-media files too.
func attachOther(units []Unit, otherByDir map[string][]File) {
	byDir := map[string][]int{}
	for i, u := range units {
		byDir[u.Directory] = append(byDir[u.Directory], i)
	}
	for dir, files := range otherByDir {
		indexes, ok := byDir[dir]
		if !ok {
			continue
		}
		// Several units can share one directory (a flat two-album folder) --
		// the shared non-media evidence (a folder.jpg, an .nfo) belongs to
		// all of them equally, since nothing distinguishes which one it
		// describes.
		for _, i := range indexes {
			units[i].Other = append(units[i].Other, files...)
		}
	}
}
