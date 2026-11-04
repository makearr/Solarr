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

// A library's target need only be taken by a profile, in any tier (2026-09-30).
func TestAccepts(t *testing.T) {
	profile, err := score.Parse([]byte(lossy320))
	if err != nil {
		t.Fatal(err)
	}
	file := func(codec string, kbps int64) score.Facts {
		return score.Facts{File: score.FileFacts{AudioCodec: codec, AudioBitrate: kbps * 1000, OverallBitrate: kbps * 1000}}
	}
	for _, tc := range []struct {
		name    string
		facts   score.Facts
		accepts bool
		reason  string
	}{
		{"flac in the second tier is taken", file("flac", 900), true, ""},
		{"aac inside its range", file("aac", 256), true, ""},
		{"aac below its minimum", file("aac", 128), false, "its bitrate range is 224kbps to 320kbps"},
	} {
		accepts, reason := score.Accepts(profile, tc.facts)
		if accepts != tc.accepts || reason != tc.reason {
			t.Errorf("%s: Accepts = %v %q, want %v %q", tc.name, accepts, reason, tc.accepts, tc.reason)
		}
	}
}

// A probed file has no source, so a profile that tiers source must not call a file below its cutoff for lacking one.
func TestFitsIgnoresAnUnmeasuredTier(t *testing.T) {
	const web1080p = `{"quality_profile":"WEB 1080p","require":{"resolution":{"min":"720p","max":"1080p"}},` +
		`"compare":["resolution","source","video_codec","score"],` +
		`"tiers":{"resolution":[["1080p"],["720p"]],"source":[["webdl"],["webrip"]],"video_codec":[["h264"],["h265"]]}}`
	profile, err := score.Parse([]byte(web1080p))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		facts score.Facts
		fits  bool
	}{
		{"1080p h264 without a source", score.Facts{File: score.FileFacts{Resolution: "1080p", VideoCodec: "h264"}}, true},
		{"720p stays below", score.Facts{File: score.FileFacts{Resolution: "720p", VideoCodec: "h264"}}, false},
		{"a known worse source stays below", score.Facts{File: score.FileFacts{Resolution: "1080p", VideoCodec: "h264", Source: "webrip"}}, false},
	} {
		if fits, _ := score.Fits(profile, tc.facts); fits != tc.fits {
			t.Errorf("%s: Fits = %v, want %v", tc.name, fits, tc.fits)
		}
	}
}
