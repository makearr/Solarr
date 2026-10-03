package probe

import (
	"slices"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/platform/mediatool"
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
func attributeClaimFromMeasured(m mediatool.Measured, sidecarSubtitles []string) library.AttributeClaim {
	claim := library.AttributeClaim{
		AudioCodec:  m.AudioCodec,
		Container:   m.FormatName,
		SampleRate:  m.SampleRate,
		BitDepth:    m.BitDepth,
		Lossless:    isLosslessCodec(m.AudioCodec),
		BitrateKbps: int(m.AudioBitrate / 1000),
		Languages:   m.AudioLanguages, LyricsLanguages: m.LyricsLanguages,
	}
	if m.ChannelLayout != "" {
		claim.AudioChannels = m.ChannelLayout
	} else if m.Channels > 0 {
		claim.AudioChannels = strconv.Itoa(m.Channels)
	}
	if m.VideoCodec != "" && m.Height > 0 {
		claim.VideoCodec, claim.Resolution = videoCodecName(m.VideoCodec), resolutionOf(m.Width, m.Height)
		claim.HDR, claim.FrameRate, claim.ScanType = m.HDR, m.FrameRate, m.ScanType
		claim.Lossless = false
	}
	claim.Subtitles = mergedLanguages(m.SubtitleLanguages, sidecarSubtitles)
	// BitrateMode is deliberately left unset: ffprobe's stream bit_rate is an
	// average regardless of whether the encoding was CBR or VBR, so this
	// would be a guess dressed as a fact -- exactly what ADR-39 exists to
	// avoid. The claimed side's own release-name signal (a preset like "V0")
	// is real evidence; this is not.
	return claim
}

// resolutionOf names a picture by its lines; a wide crop keeps the class its width says.
func resolutionOf(width, height int) string {
	switch {
	case height >= 2000 || width >= 3800:
		return "2160p"
	case height >= 1000 || width >= 1800:
		return "1080p"
	case height >= 700 || width >= 1200:
		return "720p"
	case height >= 560:
		return "576p"
	case height >= 500:
		return "540p"
	case height >= 420:
		return "480p"
	}
	return "360p"
}

// videoCodecName is ffprobe's codec_name in the vocabulary a release name uses.
func videoCodecName(codec string) string {
	switch strings.ToLower(codec) {
	case "hevc":
		return "h265"
	case "mpeg2video":
		return "mpeg2"
	case "mpeg4":
		return "xvid"
	}
	return strings.ToLower(codec)
}

func mergedLanguages(lists ...[]string) []string {
	var out []string
	for _, list := range lists {
		for _, language := range list {
			if !slices.Contains(out, language) {
				out = append(out, language)
			}
		}
	}
	return out
}
