package inspect

import (
	"context"

	"github.com/makearr/Solarr/internal/platform/fsx"
)

// SplitDirectory reproduces Walker.Walk's split for a directory with no media-bearing subdirectory (so no bubbling or disc merge): classify its files and apply splitByEvidence, in Walker's unsorted emission order.
// Run's group() re-sorts, so callers matching rebuilt units to persisted import_unit rows by ascending id (several rows sharing a rel_path) need this; use Run if either precondition is unsure, and dir must be unchanged since the walk.
func SplitDirectory(ctx context.Context, fsys fsx.FS, dir string, tags TagGrouper, floorBytes int64, books bool) ([]Unit, error) {
	if floorBytes <= 0 {
		floorBytes = DefaultMediaSizeFloorBytes
	}
	media, other, _, err := readDirectory(ctx, fsys, dir, "", WalkOptions{MediaSizeFloorBytes: floorBytes, Books: books})
	if err != nil {
		return nil, err
	}

	units := splitByEvidence("", media, tags, nil)
	for i := range units {
		units[i].Other = other
	}
	return units, nil
}
