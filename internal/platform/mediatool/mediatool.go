// Package mediatool runs the external media tools ADR-30 keeps out of the binary.
// The Go binary stays pure and static; the image carries ffprobe and fpcalc, and
// they are invoked here as batched subprocesses.
package mediatool

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// ErrToolMissing reports a tool that is not on PATH. A missing tool degrades rather
// than failing the import (ADR-30), so callers test for this rather than giving up.
var ErrToolMissing = errors.New("mediatool: not on PATH")

// ErrNoEmbeddedPicture reports a file that carries no cover art of its own.
var ErrNoEmbeddedPicture = errors.New("mediatool: no embedded picture")

// Tags are the fields a file carries. **Keys are lowercased**: the same tag is
// ARTIST in Vorbis comments and artist in ID3, and a caller must not have to know
// which container it came from.
type Tags map[string]string

// First returns the value of the first name that is present, ignoring how the name
// is punctuated. One logical tag really is spelled three ways: setting MusicBrainz's
// release group id through kid3 produces MUSICBRAINZ_RELEASEGROUPID, "MUSICBRAINZ
// RELEASE GROUP ID" and "MusicBrainz Release Group Id" depending on the container
// (documentation/media-tools.md), and an alias list would only ever be as complete as
// the last tagger someone used.
func (t Tags) First(names ...string) string {
	for _, name := range names {
		wanted := squeeze(name)
		for key, value := range t {
			if value != "" && squeeze(key) == wanted {
				return value
			}
		}
	}
	return ""
}

// squeeze reduces a tag name to its letters and digits, lowercased, so the three
// spellings above collapse to one.
func squeeze(name string) string {
	var reduced strings.Builder
	reduced.Grow(len(name))
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			reduced.WriteRune(unicode.ToLower(r))
		}
	}
	return reduced.String()
}

// Measured is what the file actually is, as opposed to what a release name claims.
// ADR-39: facts win. The fields line up with media_object's columns.
type Measured struct {
	FormatName     string
	Duration       time.Duration
	OverallBitrate int64
	AudioBitrate   int64
	VideoBitrate   int64
	AudioCodec     string
	VideoCodec     string
	SampleRate     int
	// BitDepth is 0 when ffprobe reports none -- meaningful for a lossless
	// codec (FLAC, ALAC, WAV); a lossy codec decodes to float internally and
	// has no real bit depth to report (ADR-39's measured-attributes list).
	BitDepth int
	Channels int
	Width    int
	Height   int
	// AudioLanguages and LyricsLanguages are two-letter codes; empty when the file states none.
	AudioLanguages  []string
	LyricsLanguages []string
	// The facts of a video file's picture and tracks; zero for audio. HDR is "" for sdr, else dolby-vision,
	// hdr10plus, hdr10 or hlg; ChannelLayout is "5.1", "2.0" and so on; SubtitleLanguages are the embedded tracks'.
	FrameRate         float64
	ScanType          string
	HDR               string
	ChannelLayout     string
	SubtitleLanguages []string
	// CRF is the constant rate factor the encoder's own settings state, read from the file's start; 0 when it states none.
	CRF float64
}

// Probe is one file, as one invocation reported it.
type Probe struct {
	// Path is the path that was asked for, not the one the tool echoed back --
	// results are matched to requests positionally.
	Path     string
	Tags     Tags
	Measured Measured

	// Err is why this one file failed, when the rest of the batch succeeded. A
	// batch does not fail because one file in it is unreadable.
	Err error
}

// Fingerprint is one file's acoustic fingerprint (ADR-35). Chromaprint identifies
// the same audio across formats, which no hash can do.
type Fingerprint struct {
	Path        string
	Duration    time.Duration
	Chromaprint string
	// Partial marks a fingerprint fpcalc produced while still failing: it
	// decoded enough to fingerprint but hit a problem. Usable, and worth
	// knowing about, since a damaged file is exactly what fingerprinting is
	// for.
	Partial bool
	Err     error
}

// TagEdit is one file's tag changes: Fill only where the file has no value, Set wherever the
// file's value differs, Cover (a JPEG path) embedded only when the file carries no picture.
type TagEdit struct {
	Path  string
	Fill  Tags
	Set   Tags
	Cover string
}

// TagResult is one edit's outcome: the keys the file actually changed ("cover" for the
// picture), or why it could not be written.
type TagResult struct {
	Changed []string
	Err     error
}

// Tool is ADR-30's interface. Every method takes a slice: a subprocess per file on
// the caller's hot path is what ADR-28 rules out, so the batch is one call.
type Tool interface {
	// Available reports which tools are missing rather than failing obscurely at
	// import time. It is what readiness checks.
	Available(ctx context.Context) error
	ReadTags(ctx context.Context, paths []string) ([]Probe, error)
	// WriteTags answers one result per edit, in order; the error is the batch failing as a
	// whole (the tool missing, the context cancelled).
	WriteTags(ctx context.Context, edits []TagEdit) ([]TagResult, error)
	Fingerprint(ctx context.Context, paths []string) ([]Fingerprint, error)
}

// parseInt reads a numeric field ffprobe reports as a string, treating anything
// unparseable as absent -- "N/A" is a routine value for a stream that has none.
func parseInt(value string) int64 {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0
	}
	return parsed
}

// parseSeconds reads ffprobe's fractional-second duration.
func parseSeconds(value string) time.Duration {
	seconds, err := strconv.ParseFloat(value, 64)
	if err != nil || seconds < 0 {
		return 0
	}
	return time.Duration(seconds * float64(time.Second))
}
