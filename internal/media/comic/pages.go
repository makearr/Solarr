package comic

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"slices"
	"strings"

	"github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// MinPages is the fewest page images that make a folder an issue: a cover and a back are art, not a comic.
const MinPages = 3

// maxPackedBytes is the most one packed folder may weigh, the ceiling of a single issue (C10's size sanity).
const maxPackedBytes int64 = 2 << 30

// maxPackDepth bounds how deep below a source the walk looks for page folders.
const maxPackDepth = 8

// artNames are the image names a media server or a person keeps beside pages, never a page themselves.
var artNames = map[string]bool{
	"folder": true, "poster": true, "fanart": true, "banner": true, "backdrop": true, "background": true,
	"logo": true, "clearlogo": true, "clearart": true, "thumb": true, "series": true,
}

var _ batch.FolderPacker = Importer{}

// PackFolders implements batch.FolderPacker: each folder of page images below source becomes one cbz in workspace.
func (i Importer) PackFolders(ctx context.Context, source, workspace string) ([]batch.PackedFolder, error) {
	return PackPageFolders(ctx, i.config.FS, source, workspace)
}

// PackPageFolders packs every folder below source holding at least MinPages page images and no comic archive into one
// store-only cbz at the same place below workspace, named after the folder; source is only read.
func PackPageFolders(ctx context.Context, fsys fsx.FS, source, workspace string) ([]batch.PackedFolder, error) {
	var packed []batch.PackedFolder
	var failures []error
	var visit func(relative string, depth int) error
	visit = func(relative string, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		pages, dirs, err := pageFolder(fsys, path.Join(source, relative))
		if err != nil {
			failures = append(failures, err)
			return nil
		}
		if len(pages) >= MinPages {
			name := path.Base(source)
			if relative != "" {
				name = path.Base(relative)
			}
			target := path.Join(workspace, path.Dir(relative), name+".cbz")
			if err := writePages(ctx, fsys, target, pages); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				failures = append(failures, fmt.Errorf("pack %q: %w", path.Join(source, relative), err))
			} else {
				packed = append(packed, batch.PackedFolder{File: target, Pages: pagePaths(pages)})
			}
		}
		if depth >= maxPackDepth {
			return nil
		}
		for _, dir := range dirs {
			if err := visit(path.Join(relative, dir), depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit("", 0); err != nil {
		return packed, err
	}
	return packed, errors.Join(failures...)
}

// page is one image of a folder.
type page struct {
	path string
	info fsx.FileInfo
}

func pagePaths(pages []page) []string {
	paths := make([]string, len(pages))
	for index, each := range pages {
		paths[index] = each.path
	}
	return paths
}

// pageFolder is a folder's page images in reading order, or none when it holds a comic archive (its images are extras
// then), and the subfolders worth walking.
func pageFolder(fsys fsx.FS, absolute string) ([]page, []string, error) {
	entries, err := fsys.ReadDir(absolute)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("read %q: %w", absolute, err)
	}
	var pages []page
	var dirs []string
	holdsArchive := false
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name, ".") {
			continue
		}
		if entry.IsDir {
			if entry.Name != inspect.RecycleFolder && entry.Name != "recycle-bin" {
				dirs = append(dirs, entry.Name)
			}
			continue
		}
		if Includes(entry.Name) {
			holdsArchive = true
		}
		if !isPage(entry.Name) {
			continue
		}
		// Never a symlink: what it points at is not the download's to pack (ADR-11).
		info, err := fsys.Lstat(path.Join(absolute, entry.Name))
		if err != nil || info.Mode&fs.ModeSymlink != 0 || !info.Mode.IsRegular() {
			continue
		}
		pages = append(pages, page{path: path.Join(absolute, entry.Name), info: info})
	}
	slices.Sort(dirs)
	if holdsArchive {
		return nil, dirs, nil
	}
	slices.SortStableFunc(pages, func(a, b page) int { return pageOrder(path.Base(a.path), path.Base(b.path)) })
	return pages, dirs, nil
}

// isPage says a file name is a page image: an image extension and not a media server's art.
func isPage(name string) bool {
	extension := strings.ToLower(path.Ext(name))
	if imageExtensions[extension] == "" {
		return false
	}
	return !artNames[strings.ToLower(strings.TrimSuffix(name, path.Ext(name)))]
}

// pageOrder is reading order: a page named cover first, the rest as a person numbers them ("2" before "10").
func pageOrder(a, b string) int {
	coverA, coverB := isCoverName(a), isCoverName(b)
	switch {
	case coverA && !coverB:
		return -1
	case coverB && !coverA:
		return 1
	}
	return naturalCompare(a, b)
}

func isCoverName(name string) bool {
	return strings.EqualFold(strings.TrimSuffix(name, path.Ext(name)), "cover")
}

// writePages writes the pages, in order and uncompressed (images do not shrink), as one cbz published atomically.
func writePages(ctx context.Context, fsys fsx.FS, target string, pages []page) error {
	var total int64
	for _, each := range pages {
		total += each.info.Size
	}
	if total > maxPackedBytes {
		return fmt.Errorf("%d bytes of pages, more than one issue holds (%d)", total, maxPackedBytes)
	}
	if _, err := fsys.Stat(target); err == nil {
		return fmt.Errorf("%q already exists", target)
	}
	if err := fsys.MkdirAll(path.Dir(target), 0o755); err != nil {
		return err
	}
	return fsx.WriteAtomic(fsys, target, func(out io.Writer) error {
		writer := zip.NewWriter(out)
		for _, each := range pages {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := copyPage(fsys, writer, each); err != nil {
				return err
			}
		}
		return writer.Close()
	})
}

func copyPage(fsys fsx.FS, writer *zip.Writer, each page) error {
	member, err := writer.CreateHeader(&zip.FileHeader{Name: path.Base(each.path), Method: zip.Store, Modified: each.info.ModTime})
	if err != nil {
		return err
	}
	file, err := fsys.Open(each.path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	if _, err := io.Copy(member, file); err != nil {
		return fmt.Errorf("copy %q: %w", each.path, err)
	}
	return nil
}
