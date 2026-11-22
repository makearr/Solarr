package inspect

import (
	"context"
	"path"
	"slices"
	"strings"

	"github.com/makearr/Solarr/internal/platform/fsx"
)

// ListFiles is every file under root, classified: what the import decides what to do with, and what unpack looks
// for archives in.
func ListFiles(ctx context.Context, fsys fsx.FS, root string, sizeFloor int64) ([]File, error) {
	var files []File
	if err := listTree(ctx, fsys, root, "", WalkOptions{MediaSizeFloorBytes: sizeFloor}, &files); err != nil {
		return nil, err
	}
	slices.SortFunc(files, func(a, b File) int { return strings.Compare(a.RelPath, b.RelPath) })
	return files, nil
}

func listTree(ctx context.Context, fsys fsx.FS, root, relative string, opts WalkOptions, files *[]File) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	media, other, dirs, err := readDirectory(ctx, fsys, path.Join(root, relative), relative, opts)
	if err != nil {
		return err
	}
	*files = append(append(*files, media...), other...)
	for _, dir := range dirs {
		if err := listTree(ctx, fsys, root, path.Join(relative, dir.Name), opts, files); err != nil {
			return err
		}
	}
	return nil
}

// RecycleFolder is where source archives were kept before they went to the recycle bin; the walk never enters it.
const RecycleFolder = ".solarr-recycle"

// recycleBinFolder is a recycle bin's folder in a download folder (library.RecycleBinDirectory); archives imported from
// there are in it, and the walk never enters it.
const recycleBinFolder = "recycle-bin"
