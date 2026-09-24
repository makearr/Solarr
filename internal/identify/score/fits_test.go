package score_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/identify/score"
)

// Lossy 320 as migration 0012 ships it.
const lossy320 = `{"quality_profile":"Lossy 320","compare":["audio_codec","bitrate_distance","score"],` +
	`"tiers":{"audio_codec":[["mp3","aac","ogg","opus"],["flac","alac"]]},"targets":{"bitrate":[` +
	`{"when":{"audio_codec":"mp3"},"min":"256kbps","ideal":"320kbps","max":"320kbps"},` +
	`{"when":{"audio_codec":"aac"},"min":"224kbps","ideal":"256kbps","max":"320kbps"}]}}`

func TestFits(t *testing.T) {
	profile, err := score.Parse([]byte(lossy320))
	if err != nil {
		t.Fatal(err)
	}
	file := func(codec string, kbps int64) score.Facts {
		return score.Facts{File: score.FileFacts{AudioCodec: codec, AudioBitrate: kbps * 1000, OverallBitrate: kbps * 1000}}
	}
	for _, tc := range []struct {
		name   string
		facts  score.Facts
		fits   bool
		reason string
	}{
		{"aac inside its range", file("aac", 256), true, ""},
		{"mp3 at the top of its range", file("mp3", 320), true, ""},
		{"aac below its minimum", file("aac", 128), false, "its bitrate range is 224kbps to 320kbps"},
		{"flac is the second tier", file("flac", 900), false, "it prefers another audio codec to flac"},
		{"unmeasured bitrate is not held against it", file("aac", 0), true, ""},
	} {
		fits, reason := score.Fits(profile, tc.facts)
		if fits != tc.fits || reason != tc.reason {
			t.Errorf("%s: Fits = %v %q, want %v %q", tc.name, fits, reason, tc.fits, tc.reason)
		}
	}
}
