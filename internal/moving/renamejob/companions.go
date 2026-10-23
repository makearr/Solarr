package renamejob

import (
	"path"
	"strings"

	"github.com/makearr/Solarr/internal/moving/mover"
	"github.com/makearr/Solarr/internal/platform/filekind"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// companionsOf turns the changed proposals into targets with their companions, the rules in documentation/naming.md;
// known are the media files' paths on the root.
func companionsOf(fsys fsx.FS, rootPath string, known map[string]bool, proposals []proposal) []Target {
	listing := folderListing{fsys: fsys, root: rootPath, entries: map[string][]fsx.DirEntry{}}
	claimed := map[string]bool{}
	taken := map[string]bool{}
	for _, p := range proposals {
		claimed[p.file.RelPath] = true
		taken[p.named.RelPath] = true
	}
	targets := make([]Target, 0, len(proposals))
	for _, p := range proposals {
		if !p.changed() {
			continue
		}
		target := Target{
			MediaObjectID: p.file.MediaObjectID, WorkID: p.file.WorkID, SizeBytes: p.file.SizeBytes,
			CurrentRelPath: p.file.RelPath, ProposedRelPath: p.named.RelPath, Warnings: p.named.Warnings,
		}
		dir, stem := path.Dir(p.file.RelPath), stemOf(p.file.RelPath)
		newDir, newStem := path.Dir(p.named.RelPath), stemOf(p.named.RelPath)
		siblings := listing.files(dir)
		for _, entry := range siblings {
			rel := path.Join(dir, entry.Name)
			ending, ok := strings.CutPrefix(entry.Name, stem)
			if !ok || claimed[rel] || known[rel] || isMedia(rel) || !startsEnding(ending) || longerOwner(entry.Name, stem, siblings) {
				continue
			}
			dest := path.Join(newDir, newStem+ending)
			if taken[dest] {
				continue
			}
			claimed[rel], taken[dest] = true, true
			target.Companions = append(target.Companions, mover.Companion{SourceRelPath: rel, DestRelPath: dest})
		}
		targets = append(targets, target)
	}

	for _, level := range []int{0, 1} {
		for old, moved := range movedFolders(proposals, level, listing, known) {
			first, found := firstTargetUnder(targets, old)
			if !found {
				continue
			}
			for _, entry := range listing.files(old) {
				rel := path.Join(old, entry.Name)
				dest := path.Join(moved, entry.Name)
				if claimed[rel] || known[rel] || isMedia(rel) || taken[dest] {
					continue
				}
				claimed[rel], taken[dest] = true, true
				targets[first].Companions = append(targets[first].Companions, mover.Companion{SourceRelPath: rel, DestRelPath: dest})
			}
		}
	}
	return targets
}

// movedFolders answers the folders, level folders above the files, that move as a whole: every file the rename names
// in or below one goes to the same new folder, a different one, and the folder holds no media or folders besides them.
func movedFolders(proposals []proposal, level int, listing folderListing, known map[string]bool) map[string]string {
	to := map[string]string{}
	mixed := map[string]bool{}
	children := map[string]map[string]bool{}
	for _, p := range proposals {
		old, moved := ancestor(p.file.RelPath, level), ancestor(p.named.RelPath, level)
		if old == "." || moved == "." {
			continue
		}
		if seen, ok := to[old]; ok && seen != moved {
			mixed[old] = true
		}
		to[old] = moved
		if level > 0 {
			if children[old] == nil {
				children[old] = map[string]bool{}
			}
			children[old][path.Base(ancestor(p.file.RelPath, level-1))] = true
		}
		mediaIn := ancestor(p.file.RelPath, level)
		for _, entry := range listing.files(mediaIn) {
			rel := path.Join(mediaIn, entry.Name)
			if isMedia(rel) && !known[rel] {
				mixed[old] = true
			}
		}
	}
	for old, moved := range to {
		if mixed[old] || old == moved {
			delete(to, old)
			continue
		}
		if level > 0 {
			for _, entry := range listing.all(old) {
				if entry.IsDir && !children[old][entry.Name] {
					delete(to, old)
					break
				}
			}
		}
	}
	return to
}

// firstTargetUnder is the first target whose file is in or below folder.
func firstTargetUnder(targets []Target, folder string) (int, bool) {
	for index, target := range targets {
		if strings.HasPrefix(target.CurrentRelPath, folder+"/") {
			return index, true
		}
	}
	return 0, false
}

// longerOwner says another media file beside it, identified or not, has a longer name that the entry also begins
// with: it is that file's.
func longerOwner(entry, stem string, siblings []fsx.DirEntry) bool {
	for _, sibling := range siblings {
		if !isMedia(sibling.Name) {
			continue
		}
		other := stemOf(sibling.Name)
		if len(other) > len(stem) && strings.HasPrefix(entry, other) && startsEnding(entry[len(other):]) {
			return true
		}
	}
	return false
}

// startsEnding says what follows a file's name begins a companion's ending: an extension or a "-thumb"-like suffix.
func startsEnding(ending string) bool {
	return strings.HasPrefix(ending, ".") || strings.HasPrefix(ending, "-")
}

func isMedia(rel string) bool {
	kind := filekind.Of(rel)
	return kind == filekind.Audio || kind == filekind.Video
}

func stemOf(rel string) string {
	base := path.Base(rel)
	return strings.TrimSuffix(base, path.Ext(base))
}

// ancestor is the folder level folders above the file's own (0: its own).
func ancestor(rel string, level int) string {
	dir := path.Dir(rel)
	for range level {
		dir = path.Dir(dir)
	}
	return dir
}

// folderListing reads each folder once.
type folderListing struct {
	fsys    fsx.FS
	root    string
	entries map[string][]fsx.DirEntry
}

func (l folderListing) all(dir string) []fsx.DirEntry {
	if entries, read := l.entries[dir]; read {
		return entries
	}
	entries, err := l.fsys.ReadDir(path.Join(l.root, dir))
	if err != nil {
		entries = nil
	}
	l.entries[dir] = entries
	return entries
}

func (l folderListing) files(dir string) []fsx.DirEntry {
	var files []fsx.DirEntry
	for _, entry := range l.all(dir) {
		if !entry.IsDir {
			files = append(files, entry)
		}
	}
	return files
}
