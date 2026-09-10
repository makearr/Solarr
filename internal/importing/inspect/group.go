package inspect

import (
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// discSubfolderRe recognises the disc-subfolder pattern (section 11.9): "CD1",
// "Disc 2", "Disk-3", "Vol. 4", "Volume4".
var discSubfolderRe = regexp.MustCompile(`(?i)^(cd|dis[ck]|vol(?:ume)?)\.?\s*[-_]?\s*\d+$`)

// leadingTrackNumberRe pulls a track number off the front of a filename --
// "01 - Title.flac", "01_title.mp3", "1.flac" all match.
var leadingTrackNumberRe = regexp.MustCompile(`^0*(\d{1,3})\b`)

// group partitions files' media into units. Non-media files are attached to
// whichever unit shares their directory, or dropped if no unit claims that
// directory -- Run's caller sees them via Session, not lost, since a stray
// cover.jpg beside no album is still evidence, not an import failure.
func group(files []File, tags TagReader) []Unit {
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
func splitByEvidence(directory string, files []File, tags TagReader, extra *Signal) []Unit {
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
func splitFiles(files []File, tags TagReader, structuralFallbackAllowed bool) (groups [][]File, signals [][]Signal, ambiguous bool) {
	if tags != nil {
		if g, s, ok := splitByTags(files, tags); ok {
			return g, s, false
		}
	}
	if structuralFallbackAllowed {
		if g, s, ok := splitByTrackContinuity(files); ok {
			return g, s, true
		}
	}
	return [][]File{files}, [][]Signal{{{Kind: SignalDirectoryBoundary}}}, false
}

// splitByTags groups by embedded release id first, falling back to
// (album, album-artist) when no file carries a release id. ok is false when
// no file in files has any tags at all, so the caller falls through to the
// structural signal instead of pretending one untagged group is agreement.
func splitByTags(files []File, tags TagReader) (groups [][]File, signals [][]Signal, ok bool) {
	type key struct{ releaseID, album, artist string }
	order := []key{}
	byKey := map[key][]File{}
	anyTagged := false

	for _, f := range files {
		t := tags(f.RelPath)
		if !t.Found {
			continue
		}
		anyTagged = true
		k := key{releaseID: t.ReleaseID}
		if k.releaseID == "" {
			k.album, k.artist = t.Album, t.AlbumArtist
		}
		if _, seen := byKey[k]; !seen {
			order = append(order, k)
		}
		byKey[k] = append(byKey[k], f)
	}
	if !anyTagged {
		return nil, nil, false
	}

	for _, k := range order {
		groups = append(groups, byKey[k])
		if k.releaseID != "" {
			signals = append(signals, []Signal{{Kind: SignalEmbeddedID, Detail: k.releaseID}})
		} else {
			signals = append(signals, []Signal{{Kind: SignalTagAgreement, Detail: k.artist + " / " + k.album}})
		}
	}
	return groups, signals, true
}

// splitByTrackContinuity is the one purely-structural splitter: track numbers
// that restart (drop back down rather than keep climbing) mark a new group.
// ok is false when fewer than two files carry a parseable track number, since
// one data point proves nothing.
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
	for i, f := range sorted {
		match := leadingTrackNumberRe.FindStringSubmatch(path.Base(f.RelPath))
		if match == nil {
			ordered[i] = numbered{file: f}
			continue
		}
		n, _ := strconv.Atoi(match[1])
		ordered[i] = numbered{file: f, number: n, has: true}
	}

	numberedCount := 0
	for _, o := range ordered {
		if o.has {
			numberedCount++
		}
	}
	if numberedCount < 2 {
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
