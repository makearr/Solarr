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
)

// ErrToolMissing reports a tool that is not on PATH. A missing tool degrades rather
// than failing the import (ADR-30), so callers test for this rather than giving up.
var ErrToolMissing = errors.New("mediatool: not on PATH")

// ErrWriteUnsupported reports the tag write-back path, which is deliberately not
// implemented: nothing needs it before section 19.5's write-back at stage 5, and the
// tool to do it with is still undecided (documentation/media-tools.md).
var ErrWriteUnsupported = errors.New("mediatool: writing tags is not implemented yet")

// Tags are the fields a file carries. **Keys are lowercased**: the same tag is
// ARTIST in Vorbis comments and artist in ID3, and a caller must not have to know
// which container it came from.
type Tags map[string]string

// First returns the value of the first name that is present, which is how one
// logical field is read across containers that spell it differently.
func (t Tags) First(names ...string) string {
	for _, name := range names {
		if value, ok := t[strings.ToLower(name)]; ok && value != "" {
			return value
		}
	}
	return ""
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
	Channels       int
	Width          int
	Height         int
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
	Err         error
}

// TagEdit is one file's pending tag changes. Write-back fills gaps only and never
// overwrites (section 19.5), which is the caller's rule to apply, not this one's.
type TagEdit struct {
	Path string
	Set  Tags
}

// Tool is ADR-30's interface. Every method takes a slice: a subprocess per file on
// the caller's hot path is what ADR-28 rules out, so the batch is one call.
type Tool interface {
	// Available reports which tools are missing rather than failing obscurely at
	// import time. It is what readiness checks.
	Available(ctx context.Context) error
	ReadTags(ctx context.Context, paths []string) ([]Probe, error)
	WriteTags(ctx context.Context, edits []TagEdit) error
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
