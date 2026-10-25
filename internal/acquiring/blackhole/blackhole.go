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
	"strings"
	"time"

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
}

// scan lists the watch folder's downloads, each with its total size and last change.
func (f Folders) scan() ([]entry, error) {
	if f.Watch == "" {
		return nil, ErrNoFolder
	}
	listed, err := f.FS.ReadDir(f.Watch)
	if err != nil {
		return nil, fmt.Errorf("blackhole: read the watch folder: %w", err)
	}
	var entries []entry
	for _, item := range listed {
		if fsx.IsTemporary(item.Name) || strings.HasPrefix(item.Name, ".") {
			continue
		}
		found := entry{name: item.Name, isDir: item.IsDir}
		if err := f.measure(path.Join(f.Watch, item.Name), &found); err != nil {
			return nil, err
		}
		entries = append(entries, found)
	}
	return entries, nil
}

func (f Folders) measure(at string, into *entry) error {
	info, err := f.FS.Lstat(at)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("blackhole: stat %s: %w", path.Base(at), err)
	}
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
		if err := f.measure(path.Join(at, child.Name), into); err != nil {
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
