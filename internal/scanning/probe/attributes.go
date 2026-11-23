package probe

import (
	"slices"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/quality"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// losslessCodecs are ffprobe's codec_name values for lossless audio; a "pcm_*" prefix covers every WAV/AIFF sample format, so it is a prefix check.
var losslessCodecs = map[string]bool{
	"flac": true, "alac": true, "ape": true, "wavpack": true, "tta": true,
}

func isLosslessCodec(codec string) bool {
	codec = strings.ToLower(codec)
	return losslessCodecs[codec] || strings.HasPrefix(codec, "pcm_")
}

// attributeClaimFromMeasured maps ADR-39's measured facts onto AttributeClaim, the shape the claimed side will use; both intern through media_attributes, so one converter per side suffices.
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
		claim.VideoCodec, claim.Resolution = videoCodecName(m.VideoCodec), quality.ResolutionOf(m.Width, m.Height)
		claim.HDR, claim.FrameRate, claim.ScanType = m.HDR, m.FrameRate, m.ScanType
		claim.CRF = m.CRF
		claim.Lossless = false
	}
	claim.Subtitles = mergedLanguages(m.SubtitleLanguages, sidecarSubtitles)
	// BitrateMode is left unset: ffprobe's bit_rate is an average for CBR and VBR alike, so setting it would be a guess dressed as fact (ADR-39); the claimed side's release-name preset is the real evidence.
	return claim
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
