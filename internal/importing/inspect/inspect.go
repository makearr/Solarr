// Package inspect turns one filesystem path into import units (ADR-48, section 11.9): walk to any depth, classify files, group media by evidence; it has no media-type knowledge or database (ADR-23), and identity, shaping and the commit decision are the caller's.
package inspect

import (
	"context"
	"fmt"
	"sort"

	"github.com/makearr/Solarr/internal/platform/fsx"
)

// TagGroups is how a media type's embedded tags group a set of media files (ADR-48's decisive and high-weight
// signals); the rules are the media type's (ADR-23), this package only applies them.
type TagGroups struct {
	// Groups holds indexes into the paths asked about, each exactly once.
	Groups [][]int
	// Signals is the evidence for each group, parallel to Groups.
	Signals   []Signal
	Ambiguous bool
}

// TagGrouper groups media files, by path relative to the inspected root, by their embedded tags; ok is false
// when no file carries a grouping tag. A nil TagGrouper means no tag evidence: a pure structural inspection.
type TagGrouper func(relPaths []string) (groups TagGroups, ok bool)

// Options tunes one Run. Every field is optional.
type Options struct {
	Tags TagGrouper
	// MediaSizeFloorBytes is the minimum size for a media-extension file to classify as Media rather than Junk (ADR-48's "sample.mp3"); zero uses DefaultMediaSizeFloorBytes.
	MediaSizeFloorBytes int64
	// Books counts ebook files as media, for a book library's import.
	Books bool
}

// Session is everything one Run found under one root.
type Session struct {
	Root string
	// Units is every group of media files Run believes belongs together.
	Units []Unit
}

// Run walks root, classifies every file it finds, and groups the media files into
// units. It performs no writes and resolves no identity -- see the package doc
// comment for what happens next.
func Run(ctx context.Context, fsys fsx.FS, root string, opts Options) (Session, error) {
	var units []Unit
	_, err := NewWalker().Walk(ctx, fsys, root, WalkState{}, WalkOptions{
		Tags: opts.Tags, MediaSizeFloorBytes: opts.MediaSizeFloorBytes, Books: opts.Books,
	}, func(_ context.Context, cluster []Unit) error {
		units = append(units, cluster...)
		return nil
	})
	if err != nil {
		return Session{}, fmt.Errorf("inspect: walk %q: %w", root, err)
	}
	sort.Slice(units, func(i, j int) bool {
		if units[i].Directory != units[j].Directory {
			return units[i].Directory < units[j].Directory
		}
		return firstPath(units[i].Media) < firstPath(units[j].Media)
	})
	return Session{Root: root, Units: units}, nil
}
