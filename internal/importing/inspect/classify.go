package inspect

import (
	"path"
	"regexp"
	"strings"
	"time"
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

// mediaExtensions is deliberately audio-and-video-only at this stage: books,
// comics, ROMs and every other media type add their own vocabulary when their own
// stage wires a caller into this package (ADR-23 -- growing this list is not
// scope creep, it is exactly the extension point working as intended).
var mediaExtensions = map[string]bool{
	".flac": true, ".mp3": true, ".m4a": true, ".aac": true, ".ogg": true, ".opus": true,
	".wav": true, ".aiff": true, ".aif": true, ".ape": true, ".wv": true, ".alac": true,
	".dsf": true, ".dff": true,
	".mp4": true, ".mkv": true, ".avi": true, ".mov": true, ".m4v": true, ".ts": true, ".webm": true,
}

var archiveExtensions = map[string]bool{
	".zip": true, ".rar": true, ".7z": true, ".tar": true, ".gz": true, ".bz2": true,
	".xz": true, ".cbz": true, ".cbr": true,
}

var artworkExtensions = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".bmp": true, ".gif": true,
}

var sidecarExtensions = map[string]bool{
	".cue": true, ".nfo": true, ".m3u": true, ".m3u8": true, ".log": true,
}

var subtitleExtensions = map[string]bool{
	".srt": true, ".vtt": true, ".ass": true, ".ssa": true, ".sub": true, ".idx": true,
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
	switch {
	case mediaExtensions[ext]:
		if sizeBytes < floor {
			return ClassJunk
		}
		return ClassMedia
	case archiveExtensions[ext]:
		return ClassArchive
	case artworkExtensions[ext]:
		return ClassArtwork
	case sidecarExtensions[ext]:
		return ClassSidecar
	case subtitleExtensions[ext]:
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
