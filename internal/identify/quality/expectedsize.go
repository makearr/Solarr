package quality

import (
	"math"
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
	// Notes say what each tag that is not on the target differs in.
	Notes []FitNote
}

// FitNote is one difference behind a tag's fit: the tag, the dimension that differs, and the wanted and measured values.
type FitNote struct {
	Tag, Dimension, Want, Have string
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
		if fits.Resolution != FitOK && fits.Resolution != "" {
			fits.note("resolution", "resolution", target.Resolution, file.Resolution)
		}
	}
	if target.VideoCodec != "" && file.VideoCodec != "" {
		fits.VideoCodec = FitOff
		fits.note("video_codec", "video codec", target.VideoCodec, file.VideoCodec)
		if strings.EqualFold(target.VideoCodec, file.VideoCodec) {
			fits.Notes = fits.Notes[:len(fits.Notes)-1]
			fits.VideoCodec = FitOK
			if target.VideoBitrateKbps != nil && file.VideoBitrate > 0 &&
				Off(float64(file.VideoBitrate)/1000/float64(*target.VideoBitrateKbps)) >= farOff {
				fits.VideoCodec = FitNear
				fits.note("video_codec", "video bitrate", kbpsText(float64(*target.VideoBitrateKbps)), kbpsText(float64(file.VideoBitrate)/1000))
			}
		}
	}
	if target.AudioCodec != "" && file.AudioCodec != "" {
		fits.AudioCodec = FitOff
		fits.note("audio_codec", "audio codec", target.AudioCodec, file.AudioCodec)
		if strings.EqualFold(target.AudioCodec, file.AudioCodec) {
			fits.Notes = fits.Notes[:len(fits.Notes)-1]
			fits.AudioCodec = FitOK
			if differences := audioDifferences(target, file); len(differences) > 0 {
				fits.AudioCodec = FitNear
				for _, difference := range differences {
					fits.note("audio_codec", difference.Dimension, difference.Want, difference.Have)
				}
			}
		}
	}
	fits.AudioLanguage = languageFit(target.AudioLanguages, file.AudioLanguages, false)
	// A video with no subtitle track at all has none of the unwanted ones, so it counts as on the target.
	fits.SubtitleLanguage = languageFit(target.SubtitleLanguages, file.SubtitleLanguages, file.VideoCodec != "")
	if fits.AudioLanguage != "" && fits.AudioLanguage != FitOK {
		fits.note("audio_language", "audio language", strings.Join(target.AudioLanguages, ", "), strings.Join(knownLanguages(file.AudioLanguages), ", "))
	}
	if fits.SubtitleLanguage != "" && fits.SubtitleLanguage != FitOK {
		fits.note("subtitle_language", "subtitle language", strings.Join(target.SubtitleLanguages, ", "), strings.Join(knownLanguages(file.SubtitleLanguages), ", "))
	}
	return fits
}

func (fits *TagFits) note(tag, dimension, want, have string) {
	fits.Notes = append(fits.Notes, FitNote{Tag: tag, Dimension: dimension, Want: want, Have: have})
}

func kbpsText(kbps float64) string {
	return strconv.FormatFloat(math.Round(kbps), 'f', -1, 64) + " kbit/s"
}

// knownLanguages drops und, which says nothing about a track.
func knownLanguages(have []string) []string {
	var known []string
	for _, present := range have {
		if !strings.EqualFold(present, "und") {
			known = append(known, present)
		}
	}
	return known
}

// languageFit is ok when every language of the file is wanted, near when a wanted one comes with unwanted ones, off when
// none is wanted; empty when nothing is wanted or the file states no known language (und is ignored). With noneIsFine a
// file without any language is ok, since it holds nothing unwanted.
func languageFit(want, have []string, noneIsFine bool) Fit {
	have = knownLanguages(have)
	if len(want) == 0 {
		return ""
	}
	if len(have) == 0 {
		if noneIsFine {
			return FitOK
		}
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

// audioDifferences are the ways the right audio codec comes at another channel layout, sample rate or bitrate.
func audioDifferences(target settings.QualityTarget, file score.FileFacts) []FitNote {
	var differences []FitNote
	if target.AudioChannels != "" && file.AudioChannels != "" && target.AudioChannels != file.AudioChannels {
		differences = append(differences, FitNote{Dimension: "channels", Want: target.AudioChannels, Have: file.AudioChannels})
	}
	if target.SampleRateHz > 0 && file.SampleRate > 0 && Off(float64(file.SampleRate)/float64(target.SampleRateHz)) >= nearOff {
		differences = append(differences, FitNote{Dimension: "sample rate", Want: strconv.FormatInt(target.SampleRateHz, 10) + " Hz", Have: strconv.Itoa(file.SampleRate) + " Hz"})
	}
	if target.AudioBitrateKbps != nil && file.AudioBitrate > 0 &&
		Off(float64(file.AudioBitrate)/1000/float64(*target.AudioBitrateKbps)) >= farOff {
		differences = append(differences, FitNote{Dimension: "audio bitrate", Want: kbpsText(float64(*target.AudioBitrateKbps)), Have: kbpsText(float64(file.AudioBitrate) / 1000)})
	}
	if target.BitrateKbps != nil && !target.IsVideo() && file.AudioBitrate > 0 &&
		Off(float64(file.AudioBitrate)/1000/float64(*target.BitrateKbps)) >= farOff {
		differences = append(differences, FitNote{Dimension: "bitrate", Want: kbpsText(float64(*target.BitrateKbps)), Have: kbpsText(float64(file.AudioBitrate) / 1000)})
	}
	return differences
}

// resolutionHeight is the lines of a label such as "1080p"; 0 when it has none.
func resolutionHeight(label string) int {
	height, err := strconv.Atoi(strings.TrimSuffix(strings.ToLower(label), "p"))
	if err != nil {
		return 0
	}
	return height
}
