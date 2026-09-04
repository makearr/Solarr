package probe

import (
	"strconv"
	"strings"

	"github.com/makearr/MediaNexus/internal/library"
	"github.com/makearr/MediaNexus/internal/mediatool"
)

// losslessCodecs are ffprobe's own codec_name values for a lossless audio
// stream -- a raw "pcm_*" prefix covers every sample format/endianness WAV
// and AIFF report separately (pcm_s16le, pcm_s24be, ...), so it is a prefix
// check rather than an exhaustive list of them.
var losslessCodecs = map[string]bool{
	"flac": true, "alac": true, "ape": true, "wavpack": true, "tta": true,
}

func isLosslessCodec(codec string) bool {
	codec = strings.ToLower(codec)
	return losslessCodecs[codec] || strings.HasPrefix(codec, "pcm_")
}

// attributeClaimFromMeasured maps ADR-39's measured facts onto the same
// AttributeClaim shape the claimed side will use once it is wired
// (private/next.md's own remaining item) -- both halves intern through
// media_attributes, so one converter per side is all this needs.
func attributeClaimFromMeasured(m mediatool.Measured) library.AttributeClaim {
	claim := library.AttributeClaim{
		AudioCodec:  m.AudioCodec,
		Container:   m.FormatName,
		SampleRate:  m.SampleRate,
		BitDepth:    m.BitDepth,
		Lossless:    isLosslessCodec(m.AudioCodec),
		BitrateKbps: int(m.AudioBitrate / 1000),
	}
	if m.Channels > 0 {
		claim.AudioChannels = strconv.Itoa(m.Channels)
	}
	// BitrateMode is deliberately left unset: ffprobe's stream bit_rate is an
	// average regardless of whether the encoding was CBR or VBR, so this
	// would be a guess dressed as a fact -- exactly what ADR-39 exists to
	// avoid. The claimed side's own release-name signal (a preset like "V0")
	// is real evidence; this is not.
	return claim
}
