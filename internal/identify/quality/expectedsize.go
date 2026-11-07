package quality

import (
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/score"
)

const (
	// containerOverhead is what muxing adds to the streams' own bitrates (mkv and mp4 land at one to two percent).
	containerOverhead = 0.015
	// subtitleTrackKbps is what each wanted subtitle track adds; text subtitles are far under it, image ones over.
	subtitleTrackKbps = 1.0
)

// ExpectedKbps is the overall bitrate a file on the target would have: its video and audio bitrates plus the container's and
// the subtitles' overhead. ok is false when the target names no bitrate to build it from.
func ExpectedKbps(target settings.QualityTarget) (kbps float64, ok bool) {
	var streams float64
	switch {
	case target.IsVideo():
		if target.VideoBitrateKbps == nil {
			return 0, false
		}
		streams = float64(*target.VideoBitrateKbps)
		if target.AudioBitrateKbps != nil {
			streams += float64(*target.AudioBitrateKbps)
		}
	case target.BitrateKbps != nil:
		streams = float64(*target.BitrateKbps)
	default:
		return 0, false
	}
	if streams <= 0 {
		return 0, false
	}
	return streams*(1+containerOverhead) + subtitleTrackKbps*float64(len(target.SubtitleLanguages)), true
}

// SizeRatio is how many times the expected bitrate the file's actual overall bitrate is: 1 on the target, 6.18 for a file
// six times as large, 0.5 for half. ok is false when either side is unknown.
func SizeRatio(target settings.QualityTarget, actualKbps float64) (ratio, expectedKbps float64, ok bool) {
	expected, known := ExpectedKbps(target)
	if !known || actualKbps <= 0 {
		return 0, 0, false
	}
	return actualKbps / expected, expected, true
}

// Fit says how a tag of a file stands against the target: empty when the target names nothing there.
type Fit string

// The fits a tag can have.
const (
	FitOK   Fit = "ok"
	FitNear Fit = "near"
	FitOff  Fit = "off"
)

// TagFits is the fit of each tag a file shows, against the library's target.
type TagFits struct {
	Resolution, VideoCodec, AudioCodec, AudioLanguage, SubtitleLanguage Fit
}

// Off is the share a number is away from its target, equal for a factor and its inverse: half and double are both 0.5.
func Off(ratio float64) float64 {
	if ratio <= 0 {
		return 1
	}
	if ratio > 1 {
		ratio = 1 / ratio
	}
	return 1 - ratio
}

const (
	// nearOff is the share a number may be off and still count as on the target.
	nearOff = 0.10
	// farOff is the share past which a number is far from the target.
	farOff = 0.25
)

func fitOf(off float64) Fit {
	switch {
	case off < nearOff:
		return FitOK
	case off < farOff:
		return FitNear
	}
	return FitOff
}

// Fits judges the tags of a file against a target. A subtitle tag is left out when the file
// has no subtitles at all, and a codec is near when it is the target's with another bitrate, channel layout or sample rate.
func Fits(target settings.QualityTarget, file score.FileFacts) TagFits {
	var fits TagFits
	if want := resolutionHeight(target.Resolution); want > 0 && file.Resolution != "" {
		// The label already allows for a wide crop, so a file of the target's label is on it whatever its lines.
		have := float64(file.Height)
		if have <= 0 || strings.EqualFold(file.Resolution, target.Resolution) {
			have = float64(resolutionHeight(file.Resolution))
		}
		if have > 0 {
			fits.Resolution = fitOf(Off(have / float64(want)))
		}
		if strings.EqualFold(file.Resolution, target.Resolution) {
			fits.Resolution = FitOK
		}
	}
	if target.VideoCodec != "" && file.VideoCodec != "" {
		fits.VideoCodec = FitOff
		if strings.EqualFold(target.VideoCodec, file.VideoCodec) {
			fits.VideoCodec = FitOK
			if target.VideoBitrateKbps != nil && file.VideoBitrate > 0 &&
				Off(float64(file.VideoBitrate)/1000/float64(*target.VideoBitrateKbps)) >= farOff {
				fits.VideoCodec = FitNear
			}
		}
	}
	if target.AudioCodec != "" && file.AudioCodec != "" {
		fits.AudioCodec = FitOff
		if strings.EqualFold(target.AudioCodec, file.AudioCodec) {
			fits.AudioCodec = FitOK
			if audioDiffers(target, file) {
				fits.AudioCodec = FitNear
			}
		}
	}
	fits.AudioLanguage = languageFit(target.AudioLanguages, file.AudioLanguages)
	fits.SubtitleLanguage = languageFit(target.SubtitleLanguages, file.SubtitleLanguages)
	return fits
}

// languageFit is ok when every language of the file is wanted, near when a wanted one comes with unwanted ones, off when
// none is wanted; empty when the target wants none or the file states no known language (und is ignored).
func languageFit(want, have []string) Fit {
	var known []string
	for _, present := range have {
		if !strings.EqualFold(present, "und") {
			known = append(known, present)
		}
	}
	have = known
	if len(want) == 0 || len(have) == 0 {
		return ""
	}
	wanted := 0
	for _, present := range have {
		for _, language := range want {
			if strings.EqualFold(language, present) {
				wanted++
				break
			}
		}
	}
	switch {
	case wanted == 0:
		return FitOff
	case wanted < len(have):
		return FitNear
	}
	return FitOK
}

// audioDiffers is the right audio codec at another channel layout, sample rate or bitrate.
func audioDiffers(target settings.QualityTarget, file score.FileFacts) bool {
	if target.AudioChannels != "" && file.AudioChannels != "" && target.AudioChannels != file.AudioChannels {
		return true
	}
	if target.SampleRateHz > 0 && file.SampleRate > 0 && Off(float64(file.SampleRate)/float64(target.SampleRateHz)) >= nearOff {
		return true
	}
	if target.AudioBitrateKbps != nil && file.AudioBitrate > 0 &&
		Off(float64(file.AudioBitrate)/1000/float64(*target.AudioBitrateKbps)) >= farOff {
		return true
	}
	return target.BitrateKbps != nil && !target.IsVideo() && file.AudioBitrate > 0 &&
		Off(float64(file.AudioBitrate)/1000/float64(*target.BitrateKbps)) >= farOff
}

// resolutionHeight is the lines of a label such as "1080p"; 0 when it has none.
func resolutionHeight(label string) int {
	height, err := strconv.Atoi(strings.TrimSuffix(strings.ToLower(label), "p"))
	if err != nil {
		return 0
	}
	return height
}
