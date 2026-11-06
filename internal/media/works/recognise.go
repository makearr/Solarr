package works

import (
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/identify/parsetv"
	"github.com/makearr/Solarr/internal/media"
)

// RecogniseVideoRelease lists what a video release name claims of its picture and sound; a dimension it does not state is left out.
func RecogniseVideoRelease(title string) []media.ReleaseClaim {
	claims := parsetv.ParseClaims(title)
	var out []media.ReleaseClaim
	add := func(key, value string) {
		if value != "" {
			out = append(out, media.ReleaseClaim{Key: key, Value: value})
		}
	}
	if claims.Resolution != 0 {
		add("resolution", strconv.Itoa(claims.Resolution)+"p")
	}
	add("source", claims.Source)
	if claims.Remux {
		add("remux", "yes")
	}
	add("video_codec", claims.VideoCodec)
	add("hdr", claims.HDR)
	add("audio_codec", claims.AudioCodec)
	add("channels", claims.AudioChannels)
	add("service", claims.Service)
	add("languages", strings.Join(claims.Languages, ", "))
	return out
}
