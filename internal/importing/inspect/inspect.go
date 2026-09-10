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

	"github.com/makearr/MediaNexus/internal/platform/fsx"
)

// Tags is what a caller can tell this package about one file's embedded metadata,
// when it has already read it. A zero Tags (Found false) means "not read" rather
// than "read and empty" -- the two must not be confused, or a file with genuinely
// blank tags would be treated as untagged and grouped by structure alone.
type Tags struct {
	Found       bool
	Artist      string
	Album       string
	AlbumArtist string
	// ReleaseID is the embedded release-group or release id (e.g.
	// MUSICBRAINZ_ALBUMID) -- ADR-48's decisive grouping signal when two files
	// agree on it, whatever the directory layout says.
	ReleaseID   string
	DiscNumber  int
	TrackNumber int
}

// TagReader answers one media file's tags, by its path relative to the inspected
// root. A nil TagReader means no tag evidence is available at all, which is a
// legitimate input (a pure structural inspection), not an error.
type TagReader func(relPath string) Tags

// SegmentPlausibility scores one path segment's chance of being a real entity name
// (an artist, an album) rather than a container a caller's media type had no reason
// to name ("downloads", "_unsorted"). ok is false when the caller has no opinion,
// which falls back to the built-in generic word list. Keeping this an injected
// function rather than a hardcoded vocabulary is what keeps this package free of
// media-type knowledge (ADR-23) -- a caller with real linguistic knowledge (does
// this parse as "Artist Name"?) scores better than this package ever could alone.
type SegmentPlausibility func(segment string) (confidence float64, ok bool)

// Options tunes one Run. Every field is optional.
type Options struct {
	Tags             TagReader
	SegmentPlausible SegmentPlausibility
	// MediaSizeFloorBytes is the minimum size a file with a media extension
	// must clear to classify as Media rather than Junk -- ADR-48's own example,
	// "sample.mp3", is exactly what this guards against. Zero uses
	// DefaultMediaSizeFloorBytes.
	MediaSizeFloorBytes int64
}

// Session is everything one Run found under one root.
type Session struct {
	Root string
	// Units is every group of media files Run believes belongs together.
	Units []Unit
	// ContainerSegments is every path segment Run found identical across every
	// unit beneath it, or scored implausible by Options.SegmentPlausible --
	// the "downloads/misc must never become an artist" rule, surfaced so an
	// IDENTIFY stage can skip these segments as evidence.
	ContainerSegments []string
}

// Run walks root, classifies every file it finds, and groups the media files into
// units. It performs no writes and resolves no identity -- see the package doc
// comment for what happens next.
func Run(ctx context.Context, fsys fsx.FS, root string, opts Options) (Session, error) {
	if opts.MediaSizeFloorBytes <= 0 {
		opts.MediaSizeFloorBytes = DefaultMediaSizeFloorBytes
	}

	files, err := walk(ctx, fsys, root, opts.MediaSizeFloorBytes)
	if err != nil {
		return Session{}, fmt.Errorf("inspect: walk %q: %w", root, err)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].RelPath < files[j].RelPath })

	units := group(files, opts.Tags)
	containers := containerSegments(units, opts.SegmentPlausible)

	return Session{Root: root, Units: units, ContainerSegments: containers}, nil
}
