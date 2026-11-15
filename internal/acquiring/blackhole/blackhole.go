// Package blackhole is Sonarr's Torrent and Usenet Blackhole behind download.TorrentClient and
// download.UsenetClient (ADR-46): Solarr writes what it is sent into a folder some other program watches, and finds
// the finished downloads in a second folder. There is no API: a download's reference is the name its files arrive
// under, and one whose files have not changed for a minute is finished.
package blackhole

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// settledAfter is how long a download's files must stay unchanged before it counts as finished, as Sonarr waits.
const settledAfter = time.Minute

// ErrNoFolder is a blackhole without one of its two folders.
var ErrNoFolder = errors.New("blackhole: set both the drop folder and the watch folder")

// Folders are a blackhole's two folders, as Solarr sees them.
type Folders struct {
	FS    fsx.FS
	Clock clock.Clock
	// Drop is where Solarr writes what it is sent; Watch is where the finished downloads appear.
	Drop  string
	Watch string
	// Memo keeps what a finished download measured at, so a poll does not walk every settled folder again; optional.
	Memo *Memo
}

// Memo remembers the measured downloads of each watch folder between polls, which build their client anew each time.
type Memo struct {
	mu       sync.Mutex
	measured map[string]map[string]entry
}

func (m *Memo) recall(watch string) map[string]entry {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.measured[watch]
}

// keep replaces a watch folder's memory with the entries this scan saw settled, so a removed download is forgotten.
func (m *Memo) keep(watch string, settled map[string]entry) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.measured == nil {
		m.measured = map[string]map[string]entry{}
	}
	m.measured[watch] = settled
}

var (
	// duplicateSuffix is what a client appends to the folder of a download whose name is taken: ".1" to ".99".
	duplicateSuffix = regexp.MustCompile(`\.\d{1,2}$`)
	// passwordSuffix is the "{{password}}" an NZB name may end with, which a client strips.
	passwordSuffix = regexp.MustCompile(`\{\{[^}]*\}\}$`)
)

// refKey is the reference of a download: its name's letters and digits, lower-cased, without a duplicate or password
// suffix. A client renames freely (punctuation, ".1" for a name taken, a stripped password), so the name it reports
// and the name Solarr sent only agree in this key.
func refKey(name string) string {
	name = passwordSuffix.ReplaceAllString(strings.TrimSpace(name), "")
	name = duplicateSuffix.ReplaceAllString(name, "")
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, name)
}

// withoutExtension is a loose file's name as a client names its job.
func withoutExtension(name string) string { return strings.TrimSuffix(name, path.Ext(name)) }

// namesOf lists the entries of a folder whose reference is key; a file is compared without its extension when
// extensionless says so, which is how a drop file or a usenet result names its job.
func (f Folders) namesOf(folder, key string, extensionless bool) ([]string, error) {
	listed, err := f.FS.ReadDir(folder)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("blackhole: read %s: %w", folder, err)
	}
	var names []string
	for _, item := range listed {
		if fsx.IsTemporary(item.Name) || strings.HasPrefix(item.Name, ".") {
			continue
		}
		name := item.Name
		if extensionless && !item.IsDir {
			name = withoutExtension(name)
		}
		if refKey(name) == key {
			names = append(names, item.Name)
		}
	}
	return names, nil
}

// refuseTaken refuses a release whose reference is already in either folder: the client could not tell the two apart.
func (f Folders) refuseTaken(key string, extensionlessWatch bool) error {
	for _, folder := range []struct {
		path          string
		extensionless bool
	}{{f.Drop, true}, {f.Watch, extensionlessWatch}} {
		names, err := f.namesOf(folder.path, key, folder.extensionless)
		if err != nil {
			return err
		}
		if len(names) > 0 {
			return fmt.Errorf("%w: %q is already in the blackhole", download.ErrRefused, names[0])
		}
	}
	return nil
}

// cleanName makes a release name safe as a file name.
func cleanName(name string) string {
	cleaned := strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) || r < 0x20 {
			return -1
		}
		return r
	}, name)
	return strings.Trim(strings.TrimSpace(cleaned), ".")
}

// write puts one file into the drop folder whole, never half-written where the watching program could see it.
func (f Folders) write(name string, content []byte) error {
	if f.Drop == "" || f.Watch == "" {
		return ErrNoFolder
	}
	if name == "" {
		return fmt.Errorf("blackhole: the release has no usable name")
	}
	return fsx.WriteAtomic(f.FS, path.Join(f.Drop, name), func(writer io.Writer) error {
		_, err := writer.Write(content)
		return err
	})
}

// entry is one download in the watch folder.
type entry struct {
	name   string
	isDir  bool
	size   int64
	newest time.Time
	// topModTime and topSize are the entry's own, which tell a settled download that changed from one that did not.
	topModTime time.Time
	topSize    int64
}

// scan lists the watch folder's downloads, each with its total size and last change. A settled download that has
// not changed since the last scan is not walked again.
func (f Folders) scan() ([]entry, error) {
	if f.Watch == "" {
		return nil, ErrNoFolder
	}
	listed, err := f.FS.ReadDir(f.Watch)
	if err != nil {
		return nil, fmt.Errorf("blackhole: read the watch folder: %w", err)
	}
	remembered := f.Memo.recall(f.Watch)
	settledBefore := f.Clock.Now().Add(-settledAfter)
	settled := map[string]entry{}
	var entries []entry
	for _, item := range listed {
		if fsx.IsTemporary(item.Name) || strings.HasPrefix(item.Name, ".") {
			continue
		}
		at := path.Join(f.Watch, item.Name)
		info, err := f.FS.Lstat(at)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("blackhole: stat %s: %w", item.Name, err)
		}
		if known, ok := remembered[item.Name]; ok && known.topModTime.Equal(info.ModTime) && known.topSize == info.Size {
			entries = append(entries, known)
			settled[item.Name] = known
			continue
		}
		found := entry{name: item.Name, isDir: item.IsDir, topModTime: info.ModTime, topSize: info.Size}
		if err := f.measure(at, info, &found); err != nil {
			return nil, err
		}
		entries = append(entries, found)
		if !found.newest.After(settledBefore) {
			settled[item.Name] = found
		}
	}
	f.Memo.keep(f.Watch, settled)
	return entries, nil
}

func (f Folders) measure(at string, info fsx.FileInfo, into *entry) error {
	if info.ModTime.After(into.newest) {
		into.newest = info.ModTime
	}
	if !info.IsDir {
		into.size += info.Size
		return nil
	}
	children, err := f.FS.ReadDir(at)
	if err != nil {
		return fmt.Errorf("blackhole: read %s: %w", path.Base(at), err)
	}
	for _, child := range children {
		childPath := path.Join(at, child.Name)
		childInfo, err := f.FS.Lstat(childPath)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("blackhole: stat %s: %w", path.Base(childPath), err)
		}
		if err := f.measure(childPath, childInfo, into); err != nil {
			return err
		}
	}
	return nil
}

// downloads turns the watch folder's entries into downloads; ref names each as Add answered it.
func (f Folders) downloads(ref func(entry) string) ([]download.Download, error) {
	entries, err := f.scan()
	if err != nil {
		return nil, err
	}
	settled := f.Clock.Now().Add(-settledAfter)
	downloads := make([]download.Download, len(entries))
	for i, found := range entries {
		downloads[i] = download.Download{ClientRef: ref(found), Name: found.name, State: download.StateDownloading, SizeBytes: found.size}
		if !found.newest.After(settled) {
			downloads[i].State = download.StateCompleted
			downloads[i].Path = path.Join(f.Watch, found.name)
		}
	}
	return downloads, nil
}

// remove deletes a download's files from the watch folder and what is still waiting in the drop folder. A blackhole
// can only forget a download by deleting it, as Sonarr's can.
func (f Folders) remove(watchNames, dropNames []string, deleteFiles bool) error {
	if !deleteFiles {
		return download.ErrNotSupported
	}
	// A name is a download's reference, stored and handed back by the caller: one that is not a plain entry of its
	// folder is refused before anything is deleted, so no reference can reach outside the two folders.
	for _, name := range append(slices.Clone(dropNames), watchNames...) {
		if !download.PlainName(name) {
			return fmt.Errorf("blackhole: %q is not a download in its folder", name)
		}
	}
	for _, name := range dropNames {
		if err := removeAll(f.FS, path.Join(f.Drop, name)); err != nil {
			return err
		}
	}
	for _, name := range watchNames {
		if err := removeAll(f.FS, path.Join(f.Watch, name)); err != nil {
			return err
		}
	}
	return nil
}

func removeAll(fsys fsx.FS, at string) error {
	info, err := fsys.Lstat(at)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("blackhole: stat %s: %w", path.Base(at), err)
	}
	if info.IsDir {
		children, err := fsys.ReadDir(at)
		if err != nil {
			return fmt.Errorf("blackhole: read %s: %w", path.Base(at), err)
		}
		for _, child := range children {
			if err := removeAll(fsys, path.Join(at, child.Name)); err != nil {
				return err
			}
		}
	}
	if err := fsys.Remove(at); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("blackhole: remove %s: %w", path.Base(at), err)
	}
	return nil
}

// probe proves both folders are there, and names the watch folder as where finished downloads are.
func (f Folders) probe(label string) (download.Probe, error) {
	if f.Drop == "" || f.Watch == "" {
		return download.Probe{}, ErrNoFolder
	}
	for _, folder := range []string{f.Drop, f.Watch} {
		info, err := f.FS.Stat(folder)
		if err != nil {
			return download.Probe{}, fmt.Errorf("blackhole: Solarr cannot see %s: %w", folder, err)
		}
		if !info.IsDir {
			return download.Probe{}, fmt.Errorf("blackhole: %s is not a folder", folder)
		}
	}
	return download.Probe{
		Version: label, DownloadFolder: f.Watch,
		Notes: []string{"A download counts as finished once its files have not changed for a minute."},
	}, nil
}

func folderFields(dropLabel, dropHelp string) []download.Field {
	return []download.Field{
		{Key: download.FieldDropFolder, Kind: download.FieldPath, Label: dropLabel, Help: dropHelp, Placeholder: "/blackhole/drop", Required: true},
		{
			Key: download.FieldWatchFolder, Kind: download.FieldPath, Label: "Watch folder", Placeholder: "/blackhole/done", Required: true,
			Help: "Where the finished downloads appear, as Solarr sees it; it becomes a download folder",
		},
	}
}
