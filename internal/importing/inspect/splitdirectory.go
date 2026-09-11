package inspect

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"

	"github.com/makearr/MediaNexus/internal/platform/fsx"
)

// SplitDirectory reproduces exactly what Walker.Walk's own visit does for one
// directory that has no media-bearing subdirectory of its own (so nothing
// bubbles up, and disc-subfolder merging never applies): read its own files,
// classify them, and split via the same splitByEvidence rule -- tag
// agreement first, track continuity as the only structural fallback,
// otherwise the whole directory as one group -- Walker's own per-cluster
// emission already uses.
//
// This exists to re-derive one historical WALK cluster's own unit split
// deterministically, byte for byte, when several import_unit rows share one
// rel_path (GROUP's track-continuity fallback splitting a flat multi-album
// drop). Run's own group() is not equivalent for this: it re-sorts every
// resulting unit by (directory, firstPath) before returning, which can
// differ from the order Walker actually emitted a cluster in -- Walker's
// visit calls splitByEvidence directly and emits its result unsorted. A
// caller matching reconstructed units back to persisted import_unit rows by
// position (in ascending id order, the same order AddUnits inserted them in,
// which is the same order Walker's own batch arrived in) needs Walker's own
// order, not Run's.
//
// Only meaningful for a directory Walker would never itself bubble to a
// parent (not disc-subfolder-shaped) and with no media-bearing subdirectory
// of its own -- a caller that cannot be sure both hold should use Run
// instead. dir's files must be unchanged since the original walk, or the
// split this reproduces will not match it.
func SplitDirectory(fsys fsx.FS, dir string, tags TagReader, floorBytes int64) ([]Unit, error) {
	if floorBytes <= 0 {
		floorBytes = DefaultMediaSizeFloorBytes
	}

	entries, err := fsys.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("inspect: read %q: %w", dir, err)
	}
	slices.SortFunc(entries, func(a, b fsx.DirEntry) int { return strings.Compare(a.Name, b.Name) })

	var media, other []File
	for _, entry := range entries {
		if entry.IsDir {
			continue
		}
		info, err := fsys.Stat(path.Join(dir, entry.Name))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("inspect: stat %q: %w", path.Join(dir, entry.Name), err)
		}
		file := File{
			RelPath: entry.Name, SizeBytes: info.Size, ModTime: info.ModTime,
			Class: Classify(entry.Name, info.Size, floorBytes),
		}
		if file.Class == ClassMedia {
			media = append(media, file)
		} else {
			other = append(other, file)
		}
	}

	units := splitByEvidence("", media, tags, nil)
	for i := range units {
		units[i].Other = other
	}
	return units, nil
}
