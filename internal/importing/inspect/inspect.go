// Package inspect turns one arbitrary filesystem path into the units of import it
// actually contains (ADR-48, section 11.9): walk to any depth, classify every file,
// and group the media files into candidate units by evidence rather than by depth.
// It carries no media-type knowledge and no database of its own (ADR-23) -- identity
// resolution (the ADR-24 chain), shaping a unit into SINGLE/SET/PACK and the commit
// decision are a caller's job, layered on top the way music.Consumer layers onto
// internal/scanning/scan today.
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
	// MediaSizeFloorBytes is the minimum size a file with a media extension
	// must clear to classify as Media rather than Junk -- ADR-48's own example,
	// "sample.mp3", is exactly what this guards against. Zero uses
	// DefaultMediaSizeFloorBytes.
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
	if opts.MediaSizeFloorBytes <= 0 {
		opts.MediaSizeFloorBytes = DefaultMediaSizeFloorBytes
	}

	files, err := walk(ctx, fsys, root, opts.MediaSizeFloorBytes, opts.Books)
	if err != nil {
		return Session{}, fmt.Errorf("inspect: walk %q: %w", root, err)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].RelPath < files[j].RelPath })

	units := group(files, opts.Tags)
	return Session{Root: root, Units: units}, nil
}
