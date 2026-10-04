package mediatool_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/platform/mediatool"
)

func TestEncoderCRF(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   float64
	}{
		{"x264", "\x00\x00\x01junk x264 - core 164 r3095 baee400 - H.264/MPEG-4 AVC codec - Copyleft 2003-2022 - options: cabac=1 ref=4 crf=18.0 qcomp=0.60", 18},
		{"x265", "x265 (build 199) - 3.5 - H.265/HEVC codec - options: cpuid=1 frame-threads=3 crf=21.5 aq-mode=2", 21.5},
		{"bitrate mode states no crf", "x264 - core 164 - options: cabac=1 rc=abr bitrate=2500 qcomp=0.60", 0},
		{"another encoder", "Lavc59.37.100 libx264 something crf=23", 0},
		{"crf outside the settings text is not read", "x264 - core 1 - options: cabac=1" + string(make([]byte, 5000)) + " crf=20", 0},
		{"empty", "", 0},
	}
	for _, c := range cases {
		if got := mediatool.EncoderCRF([]byte(c.header)); got != c.want {
			t.Errorf("%s: EncoderCRF = %v, want %v", c.name, got, c.want)
		}
	}
}
