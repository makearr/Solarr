package inspect

import (
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/platform/filekind"
)

// Class is what role one file plays in an import (ADR-48's classify stage).
type Class string

// Every class Classify can produce.
const (
	ClassMedia    Class = "media"
	ClassArchive  Class = "archive"
	ClassArtwork  Class = "artwork"
	ClassSidecar  Class = "sidecar"
	ClassSubtitle Class = "subtitle"
	ClassJunk     Class = "junk"
	ClassUnknown  Class = "unknown"
)

// DefaultMediaSizeFloorBytes is the minimum size a media-extension file must clear
// to classify as real media rather than junk -- small enough to keep a genuinely
// short real track, large enough to catch a "sample.mp3" or a truncated download.
// 64 KiB is below any real compressed audio second but above the debris this
// guards against.
const DefaultMediaSizeFloorBytes = 64 * 1024

var archiveExtensions = map[string]bool{
	".zip": true, ".rar": true, ".7z": true, ".tar": true, ".gz": true, ".bz2": true,
	".xz": true, ".cbz": true, ".cbr": true,
}

var sidecarExtensions = map[string]bool{
	".cue": true, ".nfo": true, ".m3u": true, ".m3u8": true, ".log": true,
}

// junkNames matches case-insensitively against the whole filename (not just the
// extension) -- these are debris every OS or download tool leaves behind, never
// content.
var junkNames = map[string]bool{
	"thumbs.db": true, "desktop.ini": true, ".ds_store": true, ".ds store": true,
}

// multipartVolumeRe recognises a multipart archive volume that its own
// extension alone would misfile, e.g. "Album.r00" or "Album.7z.001".
var multipartVolumeRe = regexp.MustCompile(`(?i)\.(r\d{2}|7z\.\d{3})$`)

// isArchivePart recognises a multipart archive volume, e.g. "Album.r00" or
// "Album.part02.rar".
func isArchivePart(name string) bool {
	lower := strings.ToLower(name)
	if multipartVolumeRe.MatchString(lower) {
		return true
	}
	return strings.Contains(lower, ".part") && strings.HasSuffix(lower, ".rar")
}

// Classify assigns one class to a file by name and size. sizeBytes below floor
// demotes an otherwise-media extension to Junk -- ADR-48's "sample.mp3" case: a
// file that looks like a track but is too small to be one.
func Classify(relPath string, sizeBytes, floor int64) Class {
	name := path.Base(relPath)
	if junkNames[strings.ToLower(name)] {
		return ClassJunk
	}
	if isArchivePart(name) {
		return ClassArchive
	}
	ext := strings.ToLower(path.Ext(name))
	kind := filekind.Of(name)
	switch {
	case kind == filekind.Audio || kind == filekind.Video:
		if sizeBytes < floor {
			return ClassJunk
		}
		return ClassMedia
	case archiveExtensions[ext]:
		return ClassArchive
	case kind == filekind.Image:
		return ClassArtwork
	case sidecarExtensions[ext]:
		return ClassSidecar
	case kind == filekind.Subtitle:
		return ClassSubtitle
	case ext == "":
		return ClassUnknown
	default:
		return ClassUnknown
	}
}

// File is one file Run found, classified.
type File struct {
	RelPath   string
	SizeBytes int64
	ModTime   time.Time
	Class     Class
}
