package inspect

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/makearr/MediaNexus/internal/platform/fsx"
)

// walk recurses to arbitrary depth under root and classifies every file it finds.
// Archive expansion (ADR-48 step 1's "expand archives") is not attempted here --
// internal/platform/archive (todo 4.10) does not exist yet, so an archive is classified and
// left unopened, same as an in-library one (ADR-49) until that lands.
func walk(ctx context.Context, fsys fsx.FS, root string, sizeFloor int64) ([]File, error) {
	var files []File
	if err := walkDir(ctx, fsys, root, "", sizeFloor, &files); err != nil {
		return nil, err
	}
	return files, nil
}

func walkDir(ctx context.Context, fsys fsx.FS, root, relative string, sizeFloor int64, files *[]File) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	absolute := root
	if relative != "" {
		absolute = path.Join(root, relative)
	}

	entries, err := fsys.ReadDir(absolute)
	if err != nil {
		return fmt.Errorf("read %q: %w", absolute, err)
	}
	slices.SortFunc(entries, func(a, b fsx.DirEntry) int { return strings.Compare(a.Name, b.Name) })

	for _, entry := range entries {
		childRelative := path.Join(relative, entry.Name)
		if entry.IsDir {
			if err := walkDir(ctx, fsys, root, childRelative, sizeFloor, files); err != nil {
				return err
			}
			continue
		}
		info, err := fsys.Stat(path.Join(absolute, entry.Name))
		if err != nil {
			return fmt.Errorf("stat %q: %w", path.Join(absolute, entry.Name), err)
		}
		*files = append(*files, File{
			RelPath: childRelative, SizeBytes: info.Size, ModTime: info.ModTime,
			Class: Classify(childRelative, info.Size, sizeFloor),
		})
	}
	return nil
}
