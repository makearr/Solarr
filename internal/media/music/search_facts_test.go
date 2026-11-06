package music

import (
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/identify/parse"
)

func TestFactsFromReleaseInfersMP3FromBareBitrate(t *testing.T) {
	cases := map[string]string{
		"Artist - Album (2008) 320 kbps": "mp3",
		"Artist - Album (2008) MP3 V0":   "mp3",
		"Artist - Album (2008) AAC 256":  "aac",
		"Artist - Album (2008) [FLAC]":   "flac",
		"Artist - Album (2008)":          "",
	}
	for name, want := range cases {
		facts := factsFromRelease(parse.Parse(name), indexer.RawItem{Title: name}, "torznab")
		if facts.File.AudioCodec != want {
			t.Errorf("%q: codec %q, want %q", name, facts.File.AudioCodec, want)
		}
	}
}
