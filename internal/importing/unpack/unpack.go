// Package unpack opens the archives of an import source into a workspace, so that an archive is inspected, grouped
// and committed exactly like a folder (ADR-49). The workspace is outside every library root; nothing is ever
// extracted next to the archive, which may sit on a read-only download mount.
package unpack

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/platform/archive"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// sizeFloor is the junk floor for classifying files here; archives are never below it.
const sizeFloor = 0

// Find lists the archives to open under root, one per set: the primary volume of a multipart set, or the single
// archive file. Paths are relative to root, in order.
func Find(ctx context.Context, fsys fsx.FS, root string) ([]string, error) {
	files, err := inspect.ListFiles(ctx, fsys, root, sizeFloor)
	if err != nil {
		return nil, err
	}
	return primaries(files), nil
}

func primaries(files []inspect.File) []string {
	byDirectory := map[string][]string{}
	for _, file := range files {
		if file.Class == inspect.ClassArchive {
			byDirectory[path.Dir(file.RelPath)] = append(byDirectory[path.Dir(file.RelPath)], path.Base(file.RelPath))
		}
	}
	var found []string
	for directory, names := range byDirectory {
		sets := archive.DetectMultipart(names)
		inSet := map[string]bool{}
		for _, set := range sets {
			found = append(found, path.Join(directory, set.Primary))
			for _, name := range names {
				if belongs(name, set) {
					inSet[name] = true
				}
			}
		}
		for _, name := range names {
			if _, readable := archive.DetectFormat(name); readable && !inSet[name] {
				found = append(found, path.Join(directory, name))
			}
		}
	}
	slices.Sort(found)
	return slices.Compact(found)
}

// belongs reports whether name is a volume of set (its primary included).
func belongs(name string, set *archive.MultipartSet) bool {
	if name == set.Primary {
		return true
	}
	lower := strings.ToLower(name)
	return strings.HasPrefix(lower, strings.ToLower(set.Base)+".")
}

// Expand opens every archive under source into its own folder of workspace, named like the archive without its
// extension, and then every archive found inside those, up to limits.MaxDepth levels. It answers how many archives
// were opened; an archive that cannot be read fails the whole expansion, so nothing half-opened is imported.
func Expand(ctx context.Context, fsys fsx.FS, source, workspace string, limits archive.Limits) (int, error) {
	archives, err := Find(ctx, fsys, source)
	if err != nil || len(archives) == 0 {
		return 0, err
	}
	opened := 0
	for _, relative := range archives {
		destination := path.Join(workspace, folderName(relative))
		count, err := open(ctx, fsys, path.Join(source, relative), destination, limits, 1)
		opened += count
		if err != nil {
			return opened, fmt.Errorf("unpack: %s: %w", relative, err)
		}
	}
	return opened, nil
}

// open extracts one archive into destination and recurses into the archives it held, in place: the workspace is
// ours, so a nested archive is replaced by the folder it opens to.
func open(ctx context.Context, fsys fsx.FS, source, destination string, limits archive.Limits, depth int) (int, error) {
	if err := fsys.MkdirAll(destination, 0o755); err != nil {
		return 0, err
	}
	if err := archive.Extract(ctx, fsys, source, destination, limits); err != nil {
		return 0, err
	}
	opened := 1
	if depth >= limits.MaxDepth {
		return opened, nil
	}
	nested, err := Find(ctx, fsys, destination)
	if err != nil {
		return opened, err
	}
	for _, relative := range nested {
		inner := path.Join(destination, relative)
		count, err := open(ctx, fsys, inner, strings.TrimSuffix(inner, path.Ext(inner)), limits, depth+1)
		opened += count
		if err != nil {
			return opened, fmt.Errorf("%s: %w", relative, err)
		}
		if err := removeVolumes(fsys, inner); err != nil {
			return opened, err
		}
	}
	return opened, nil
}

// removeVolumes deletes a nested archive's file and its other volumes once it has been opened.
func removeVolumes(fsys fsx.FS, primary string) error {
	directory := path.Dir(primary)
	entries, err := fsys.ReadDir(directory)
	if err != nil {
		return err
	}
	names := make([]string, len(entries))
	for index, entry := range entries {
		names[index] = entry.Name
	}
	remove := volumesOf(names, path.Base(primary))
	for _, name := range remove {
		if err := fsys.Remove(path.Join(directory, name)); err != nil && !isMissing(err) {
			return err
		}
	}
	return nil
}

// volumesOf lists the files of the archive whose primary volume is primary: itself plus the rest of its set.
func volumesOf(names []string, primary string) []string {
	volumes := []string{primary}
	for _, set := range archive.DetectMultipart(names) {
		if set.Primary != primary {
			continue
		}
		for _, name := range names {
			if belongs(name, set) && name != primary {
				volumes = append(volumes, name)
			}
		}
	}
	return volumes
}

func isMissing(err error) bool { return errors.Is(err, fs.ErrNotExist) }

// folderName is where an archive's contents go: its relative path without the archive extension, so two archives
// in different folders never share one.
func folderName(relative string) string {
	name := relative
	for _, suffix := range []string{".tar.gz", ".tar.bz2", ".tar.xz", ".tgz", ".tbz2", ".txz"} {
		if strings.HasSuffix(strings.ToLower(name), suffix) {
			return strings.TrimSuffix(name, name[len(name)-len(suffix):])
		}
	}
	name = strings.TrimSuffix(name, path.Ext(name))
	// "Album.part01" from "Album.part01.rar": the volume marker is not part of the folder's name.
	for _, marker := range []string{".part1", ".part01", ".part001"} {
		if strings.HasSuffix(strings.ToLower(name), marker) {
			return name[:len(name)-len(marker)]
		}
	}
	return name
}

// RemoveTree deletes a workspace, children first.
func RemoveTree(fsys fsx.FS, root string) error {
	entries, err := fsys.ReadDir(root)
	if err != nil {
		if isMissing(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		child := path.Join(root, entry.Name)
		if entry.IsDir {
			if err := RemoveTree(fsys, child); err != nil {
				return err
			}
			continue
		}
		if err := fsys.Remove(child); err != nil {
			return err
		}
	}
	return fsys.Remove(root)
}

// SweepOlderThan removes the workspaces of dir last touched before cutoff and answers how many; a missing dir is none.
func SweepOlderThan(fsys fsx.FS, dir string, cutoff time.Time) (int, error) {
	entries, err := fsys.ReadDir(dir)
	if err != nil {
		if isMissing(err) {
			return 0, nil
		}
		return 0, err
	}
	removed := 0
	for _, entry := range entries {
		folder := path.Join(dir, entry.Name)
		info, err := fsys.Stat(folder)
		if err != nil || !info.ModTime.Before(cutoff) {
			continue
		}
		if err := RemoveTree(fsys, folder); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// Retire moves every archive set of source into the source's own recycle folder, keeping its relative path: a rename
// on the source's filesystem, so nothing is copied and the bytes stay recoverable until SweepRecycled ages them out.
func Retire(ctx context.Context, fsys fsx.FS, source string) (int, error) {
	archives, err := Find(ctx, fsys, source)
	if err != nil {
		return 0, err
	}
	moved := 0
	for _, relative := range archives {
		directory := path.Dir(path.Join(source, relative))
		entries, err := fsys.ReadDir(directory)
		if err != nil {
			return moved, err
		}
		names := make([]string, len(entries))
		for index, entry := range entries {
			names[index] = entry.Name
		}
		volumes := volumesOf(names, path.Base(relative))
		target := path.Join(source, inspect.RecycleFolder, path.Dir(relative))
		if err := fsys.MkdirAll(target, 0o755); err != nil {
			return moved, err
		}
		for _, name := range volumes {
			if err := fsys.Rename(path.Join(directory, name), path.Join(target, name)); err != nil {
				return moved, err
			}
		}
		moved++
	}
	return moved, nil
}

// SweepRecycled removes what source's recycle folder holds from before cutoff, top-level entries first seen by age.
func SweepRecycled(fsys fsx.FS, source string, cutoff time.Time) (int, error) {
	return SweepOlderThan(fsys, path.Join(source, inspect.RecycleFolder), cutoff)
}
