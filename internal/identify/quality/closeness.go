package quality

import (
	"cmp"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/score"
)

// Part is one thing a target names, how close the file is to it, and what each side was.
type Part struct {
	Dimension string
	Want      string
	Have      string
	// Closeness is 1 when the file matches, falling to 0 as it gets further away.
	Closeness float64
	weight    float64
}

// Closeness is how near a file is to a target, from the parts both name; a part the probe could not read is left out.
type Closeness struct {
	Percent int
	Parts   []Part
}

// Gaps are the parts the file does not match, the furthest first.
func (c Closeness) Gaps() []Part {
	var gaps []Part
	for _, part := range c.Parts {
		if part.Closeness < 1 {
			gaps = append(gaps, part)
		}
	}
	slices.SortStableFunc(gaps, func(a, b Part) int { return cmp.Compare(a.Closeness, b.Closeness) })
	return gaps
}

// resolutionLadder orders the resolutions a target can name, so one step off costs a fixed share.
var resolutionLadder = []string{"2160p", "1440p", "1080p", "720p", "576p", "480p"}

const (
	// resolutionStepCost is what each rung between the file's resolution and the target's takes off.
	resolutionStepCost = 0.4
	// octavesToZero is how far a number may be from the target, as a factor of two, before it counts for nothing.
	octavesToZero = 2.0
	// overshootDiscount makes a number above the target of a prefer-more target cost half.
	overshootDiscount = 0.5
	// crfSlope is how much each CRF point above the target takes off.
	crfSlope = 0.1
)

// Distance judges a file against a target: unlike MeetsTarget, which is yes or no, how close it is, so a library can
// show what a transcode to the target would change. ok is false when nothing the target names could be read.
func Distance(target settings.QualityTarget, file score.FileFacts) (closeness Closeness, ok bool) {
	var parts []Part
	add := func(dimension, want, have string, weight, value float64) {
		parts = append(parts, Part{Dimension: dimension, Want: want, Have: have, Closeness: math.Max(0, math.Min(1, value)), weight: weight})
	}
	exact := func(dimension, want, have string, weight float64) {
		if want == "" || have == "" {
			return
		}
		value := 0.0
		if strings.EqualFold(want, have) {
			value = 1
		}
		add(dimension, want, have, weight, value)
	}
	number := func(dimension string, want, have, weight float64, unit string, tolerance float64) {
		if want <= 0 || have <= 0 {
			return
		}
		value := 1.0
		if math.Abs(have-want) > want*tolerance/100 {
			octaves := math.Abs(math.Log2(have / want))
			if target.PreferOver && have > want {
				octaves *= overshootDiscount
			}
			value = 1 - octaves/octavesToZero
		}
		add(dimension, formatNumber(want)+unit, formatNumber(have)+unit, weight, value)
	}
	tolerance := float64(target.TolerancePct)
	if tolerance <= 0 {
		tolerance = defaultBitrateTolerancePct
	}
	name := file.Extension
	if name == "" {
		name = file.Container
	}
	if target.Container != "" && name != "" {
		match := false
		for _, option := range strings.Split(target.Container, ",") {
			match = match || score.CanonicalContainer(option) == score.CanonicalContainer(name)
		}
		value := 0.0
		if match {
			value = 1
		}
		add("container", target.Container, name, 1, value)
	}
	if want, have := rung(target.Resolution), rung(file.Resolution); want >= 0 && have >= 0 {
		add("resolution", target.Resolution, file.Resolution, 2, 1-resolutionStepCost*math.Abs(float64(want-have)))
	}
	exact("video codec", target.VideoCodec, file.VideoCodec, 2)
	if target.HDR != "" {
		have := file.HDR
		if have == "" && file.Resolution != "" {
			have = "sdr"
		}
		exact("hdr", target.HDR, have, 1)
	}
	exact("audio codec", target.AudioCodec, file.AudioCodec, 1.5)
	exact("channels", target.AudioChannels, file.AudioChannels, 1)
	if target.SampleRateHz > 0 && file.SampleRate > 0 {
		number("sample rate", float64(target.SampleRateHz), float64(file.SampleRate), 1, " Hz", 0)
	}
	if target.BitDepth > 0 && file.BitDepth > 0 && !target.IsVideo() {
		exact("bit depth", strconv.FormatInt(target.BitDepth, 10), strconv.Itoa(file.BitDepth), 1)
	}
	if target.VideoBitrateKbps != nil {
		number("video bitrate", float64(*target.VideoBitrateKbps), float64(file.VideoBitrate)/1000, 2, " kbit/s", tolerance)
	}
	if target.AudioBitrateKbps != nil {
		number("audio bitrate", float64(*target.AudioBitrateKbps), float64(file.AudioBitrate)/1000, 1, " kbit/s", tolerance)
	}
	if target.BitrateKbps != nil && !target.IsVideo() {
		number("bitrate", float64(*target.BitrateKbps), float64(file.AudioBitrate)/1000, 2, " kbit/s", tolerance)
	}
	if target.FrameRate > 0 {
		number("frame rate", target.FrameRate, file.FrameRate, 1, " fps", frameRateMargin*100)
	}
	if target.MaxFrameRate > 0 && file.FrameRate > 0 {
		value := 1.0
		if file.FrameRate > target.MaxFrameRate*(1+frameRateMargin) {
			value = 0
		}
		add("frame rate cap", formatNumber(target.MaxFrameRate)+" fps", formatNumber(file.FrameRate)+" fps", 1, value)
	}
	if target.CRF > 0 && file.CRF > 0 {
		value := 1.0
		if file.CRF > target.CRF+crfMargin {
			value = 1 - (file.CRF-target.CRF)*crfSlope
		}
		add("CRF", formatNumber(target.CRF), formatNumber(file.CRF), 1.5, value)
	}
	if len(target.AudioLanguages) > 0 && len(file.AudioLanguages) > 0 {
		value := 0.0
		if shares(target.AudioLanguages, file.AudioLanguages) {
			value = 1
		}
		add("audio language", strings.Join(target.AudioLanguages, "/"), strings.Join(file.AudioLanguages, "/"), 1, value)
	}
	if len(parts) == 0 {
		return Closeness{}, false
	}
	var earned, possible float64
	for _, part := range parts {
		earned += part.Closeness * part.weight
		possible += part.weight
	}
	return Closeness{Percent: int(math.Round(100 * earned / possible)), Parts: parts}, true
}

// rung is the resolution's place on the ladder, or -1 when it is not on it.
func rung(resolution string) int {
	for index, name := range resolutionLadder {
		if strings.EqualFold(name, resolution) {
			return index
		}
	}
	return -1
}

func formatNumber(value float64) string {
	return strconv.FormatFloat(math.Round(value*100)/100, 'f', -1, 64)
}
