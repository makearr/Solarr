package music_test

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/music"
)

func TestRecogniseRelease_ABareBitrateIsMP3AndALosslessNameKeepsNoBitrate(t *testing.T) {
	got := music.Module{}.RecogniseRelease("Invented Artist - Invented Album (2017) 320")
	want := []media.ReleaseClaim{{Key: "format", Value: "mp3"}, {Key: "bitrate", Value: "320 kbps"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("claims = %+v, want %+v", got, want)
	}
	flac := music.Module{}.RecogniseRelease("Invented Artist - Invented Album (2017) [FLAC]")
	if len(flac) < 2 || flac[0] != (media.ReleaseClaim{Key: "format", Value: "flac"}) || flac[1] != (media.ReleaseClaim{Key: "lossless", Value: "yes"}) {
		t.Errorf("claims = %+v, want format flac then lossless", flac)
	}
}
