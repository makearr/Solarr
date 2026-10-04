package mediatool

import (
	"bytes"
	"regexp"
	"strconv"
)

// EncoderHeaderBytes is how much of a video file's start can hold the encoder's settings string: x264 and x265 write it
// in the first frame, which follows the container's own headers.
const EncoderHeaderBytes = 512 * 1024

var (
	encoderMarkers = [][]byte{[]byte("x264 - core "), []byte("x265 (build ")}
	crfSetting     = regexp.MustCompile(`\bcrf=([0-9]+(?:\.[0-9]+)?)`)
)

// maxEncoderSettings bounds the text read after a marker: the options run to about a kilobyte.
const maxEncoderSettings = 4096

// EncoderCRF is the constant rate factor an x264 or x265 encode states in its settings string, read from the start of
// the file; 0 when the file states none (another encoder, or a bitrate or quantizer mode instead).
func EncoderCRF(header []byte) float64 {
	for _, marker := range encoderMarkers {
		at := bytes.Index(header, marker)
		if at < 0 {
			continue
		}
		settings := header[at:min(len(header), at+maxEncoderSettings)]
		if found := crfSetting.FindSubmatch(settings); found != nil {
			if crf, err := strconv.ParseFloat(string(found[1]), 64); err == nil {
				return crf
			}
		}
	}
	return 0
}
