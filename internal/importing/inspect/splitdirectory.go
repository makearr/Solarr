package inspect

import (
	"context"

	"github.com/makearr/Solarr/internal/platform/fsx"
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
