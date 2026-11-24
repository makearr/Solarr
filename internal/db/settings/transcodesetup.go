package settings

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/platform/config"
)

// TranscodeEngine is what encodes a file.
type TranscodeEngine string

// The engines; auto takes the best working GPU that encodes the target's codec, else the CPU.
const (
	EngineAuto     TranscodeEngine = "auto"
	EngineSoftware TranscodeEngine = "software"
	EngineQSV      TranscodeEngine = "qsv"
	EngineVAAPI    TranscodeEngine = "vaapi"
	EngineNVENC    TranscodeEngine = "nvenc"
)

// RateMode is how video bits are spent: a constant quality or a bitrate.
type RateMode string

// The rate modes.
const (
	RateQuality RateMode = "quality"
	RateBitrate RateMode = "bitrate"
)

// EncoderPreset is how slowly an encoder works for its size.
type EncoderPreset string

// The five presets a setup accepts.
const (
	PresetFast     EncoderPreset = "fast"
	PresetNormal   EncoderPreset = "normal"
	PresetSlow     EncoderPreset = "slow"
	PresetSlower   EncoderPreset = "slower"
	PresetVerySlow EncoderPreset = "veryslow"
)

// FilterStrength is how hard a filter works.
type FilterStrength string

// The strengths.
const (
	StrengthOff    FilterStrength = "off"
	StrengthLow    FilterStrength = "low"
	StrengthMedium FilterStrength = "medium"
	StrengthStrong FilterStrength = "strong"
)

// DenoiseMethod names the filter behind a video denoise.
type DenoiseMethod string

// SharpenMethod names the filter behind a sharpen.
type SharpenMethod string

// AudioDenoiseMethod names the filter behind an audio denoise.
type AudioDenoiseMethod string

// The filters a setup can choose between.
const (
	DenoiseAuto    DenoiseMethod = "auto"
	DenoiseHQDN3D  DenoiseMethod = "hqdn3d"
	DenoiseNLMeans DenoiseMethod = "nlmeans"
	DenoiseBM3D    DenoiseMethod = "bm3d"

	SharpenCAS       SharpenMethod = "cas"
	SharpenSmartBlur SharpenMethod = "smartblur"

	AudioDenoiseFFT AudioDenoiseMethod = "afftdn"
	AudioDenoiseNLM AudioDenoiseMethod = "anlmdn"
)

// TranscodeSetupVersion is the version of the setup document.
const TranscodeSetupVersion = 1

// TranscodeSetup is how files are converted to one target quality; the target itself says to what.
type TranscodeSetup struct {
	Version int              `json:"version"`
	Encode  TranscodeEncode  `json:"encode"`
	Skip    TranscodeSkip    `json:"skip"`
	Streams TranscodeStreams `json:"streams"`
	Video   VideoFilters     `json:"video_filters"`
	Audio   AudioFilters     `json:"audio_filters"`
	Check   TranscodeCheck   `json:"check"`
	Ladder  TranscodeLadder  `json:"ladder"`
}

// TranscodeEncode is the encoder choice. Quality is in software CRF terms; HardwareQualityOffset is added for a GPU engine.
type TranscodeEncode struct {
	Engine                TranscodeEngine `json:"engine"`
	AllowCPUFallback      bool            `json:"allow_cpu_fallback"`
	RateMode              RateMode        `json:"rate_mode"`
	Quality               float64         `json:"quality"`
	HardwareQualityOffset float64         `json:"hardware_quality_offset"`
	VideoBitrateKbps      int64           `json:"video_bitrate_kbps"`
	Preset                EncoderPreset   `json:"preset"`
}

// TranscodeSkip are the rules that make a file "not needed": it is fine when every active rule passes.
type TranscodeSkip struct {
	TranscodedBefore    bool     `json:"transcoded_before"`
	ContainerMatches    bool     `json:"container_matches"`
	CodecMatches        bool     `json:"codec_matches"`
	AlsoVideoCodecs     []string `json:"also_video_codecs"`
	AlsoAudioCodecs     []string `json:"also_audio_codecs"`
	ResolutionAtOrBelow bool     `json:"resolution_at_or_below"`
	RateAtOrBelow       bool     `json:"rate_at_or_below"`
	OnlyWantedLanguages bool     `json:"only_wanted_languages"`
	WithinRange         bool     `json:"within_range"`
	RangePct            int64    `json:"range_pct"`
	BelowMax            bool     `json:"below_max"`
	MaxPct              int64    `json:"max_pct"`
}

// TranscodeStreams decides which streams are copied and which are changed.
type TranscodeStreams struct {
	CopyVideoOnTarget       bool  `json:"copy_video_on_target"`
	VideoCopyMaxPct         int64 `json:"video_copy_max_pct"`
	CopyAudioOnTarget       bool  `json:"copy_audio_on_target"`
	AudioCopyMaxPct         int64 `json:"audio_copy_max_pct"`
	Downscale               bool  `json:"downscale"`
	NoUpscale               bool  `json:"no_upscale"`
	ReduceFPS               bool  `json:"reduce_fps"`
	NoFPSIncrease           bool  `json:"no_fps_increase"`
	Downmix                 bool  `json:"downmix"`
	NoUpmix                 bool  `json:"no_upmix"`
	NoSampleRateIncrease    bool  `json:"no_sample_rate_increase"`
	RemoveUnwantedAudio     bool  `json:"remove_unwanted_audio"`
	RemoveUnwantedSubtitles bool  `json:"remove_unwanted_subtitles"`
}

// VideoFilters are applied only to a video stream that is re-encoded anyway; all start off but Deinterlace follows the target.
type VideoFilters struct {
	Deinterlace   bool           `json:"deinterlace"`
	Denoise       FilterStrength `json:"denoise"`
	DenoiseMethod DenoiseMethod  `json:"denoise_method"`
	Deblock       FilterStrength `json:"deblock"`
	Deband        FilterStrength `json:"deband"`
	Deflicker     FilterStrength `json:"deflicker"`
	Sharpen       FilterStrength `json:"sharpen"`
	SharpenMethod SharpenMethod  `json:"sharpen_method"`
	Tonemap       bool           `json:"tonemap"`
}

// AudioFilters are applied only to an audio stream that is re-encoded anyway.
type AudioFilters struct {
	Normalize     bool               `json:"normalize"`
	DialogEnhance bool               `json:"dialog_enhance"`
	Compress      bool               `json:"compress"`
	Denoise       FilterStrength     `json:"denoise"`
	DenoiseMethod AudioDenoiseMethod `json:"denoise_method"`
	Declip        bool               `json:"declip"`
}

// TranscodeCheck is what a result must satisfy before it replaces the original.
type TranscodeCheck struct {
	Duration        bool  `json:"duration"`
	DurationSeconds int64 `json:"duration_seconds"`
	Smaller         bool  `json:"smaller"`
	WithinRange     bool  `json:"within_range"`
	RangePct        int64 `json:"range_pct"`
	BelowMax        bool  `json:"below_max"`
	MaxPct          int64 `json:"max_pct"`
}

// TranscodeLadder is the automatic second passes: each extra pass lowers the quality by QualityStep or the bitrate by BitrateStepPct.
type TranscodeLadder struct {
	ExtraPasses    int64   `json:"extra_passes"`
	QualityStep    float64 `json:"quality_step"`
	BitrateStepPct int64   `json:"bitrate_step_pct"`
}

// defaultVideoQuality is the software CRF a video target without one gets, by codec and the resolution column (up to 576p, 720p, 1080p, 1440p and up).
var defaultVideoQuality = map[string][4]float64{
	"hevc": {26, 27, 28, 28}, "h264": {21, 22, 23, 23}, "av1": {32, 33, 34, 34}, "vp9": {29, 30, 31, 31},
}

// isTranscodeVideo says the target is a video target: it names a video codec or resolution, or belongs to a video type.
func (t QualityTarget) isTranscodeVideo() bool {
	return t.VideoCodec != "" || t.Resolution != "" || TargetFamily(t.MediaType) == "video"
}

// resolutionColumn is the column of defaultVideoQuality a resolution text ("1080p", "4k") falls in; 1080p when unknown.
func resolutionColumn(resolution string) int {
	text := strings.ToLower(strings.TrimSpace(resolution))
	if strings.HasPrefix(text, "4k") || strings.HasPrefix(text, "8k") {
		return 3
	}
	height, err := strconv.Atoi(strings.TrimSuffix(text, "p"))
	switch {
	case err != nil:
		return 2
	case height <= 576:
		return 0
	case height <= 720:
		return 1
	case height <= 1080:
		return 2
	}
	return 3
}

// DefaultTranscodeSetup is the setup a target has until one is saved; an audio target leaves the video parts at zero values.
func DefaultTranscodeSetup(target QualityTarget) TranscodeSetup {
	setup := TranscodeSetup{
		Version: TranscodeSetupVersion,
		Encode:  TranscodeEncode{Engine: EngineAuto, AllowCPUFallback: true, RateMode: RateQuality, HardwareQualityOffset: -3, Preset: PresetSlower},
		Skip: TranscodeSkip{
			TranscodedBefore: true, ContainerMatches: true, CodecMatches: true, ResolutionAtOrBelow: true, RateAtOrBelow: true,
			OnlyWantedLanguages: true, WithinRange: true, RangePct: 20, MaxPct: 120, AlsoVideoCodecs: []string{}, AlsoAudioCodecs: []string{},
		},
		Streams: TranscodeStreams{
			CopyVideoOnTarget: true, VideoCopyMaxPct: 110, CopyAudioOnTarget: true, AudioCopyMaxPct: 110, Downscale: true, NoUpscale: true,
			ReduceFPS: true, NoFPSIncrease: true, Downmix: true, NoUpmix: true, NoSampleRateIncrease: true, RemoveUnwantedAudio: true, RemoveUnwantedSubtitles: true,
		},
		Video:  VideoFilters{Denoise: StrengthOff, DenoiseMethod: DenoiseAuto, Deblock: StrengthOff, Deband: StrengthOff, Deflicker: StrengthOff, Sharpen: StrengthOff, SharpenMethod: SharpenCAS},
		Audio:  AudioFilters{Normalize: true, Denoise: StrengthOff, DenoiseMethod: AudioDenoiseFFT},
		Check:  TranscodeCheck{Duration: true, DurationSeconds: 1, Smaller: true, WithinRange: true, RangePct: 20, MaxPct: 120},
		Ladder: TranscodeLadder{ExtraPasses: 2, QualityStep: 2, BitrateStepPct: 15},
	}
	if !target.isTranscodeVideo() {
		setup.Encode.RateMode, setup.Encode.HardwareQualityOffset, setup.Encode.Preset = "", 0, ""
		setup.Video = VideoFilters{}
		return setup
	}
	setup.Video.Deinterlace = target.Deinterlace
	setup.Encode.VideoBitrateKbps = 0
	if target.CRF > 0 {
		setup.Encode.Quality = target.CRF
	} else if row, known := defaultVideoQuality[strings.ToLower(target.VideoCodec)]; known {
		setup.Encode.Quality = row[resolutionColumn(target.Resolution)]
	}
	if preset := EncoderPreset(strings.ToLower(target.EncoderPreset)); slices.Contains(transcodePresets, preset) {
		setup.Encode.Preset = preset
	}
	return setup
}

var transcodePresets = []EncoderPreset{PresetFast, PresetNormal, PresetSlow, PresetSlower, PresetVerySlow}

// ParseTranscodeSetup reads a stored or sent document over the target's default: a missing field keeps its default, an unknown one is refused.
func ParseTranscodeSetup(raw []byte, target QualityTarget) (TranscodeSetup, error) {
	setup := DefaultTranscodeSetup(target)
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&setup); err != nil {
		return TranscodeSetup{}, fmt.Errorf("settings: transcode setup: %w", err)
	}
	return setup, nil
}

// Validate checks the ranges and the names; a field a target's kind cannot use is ignored, not refused.
func (s TranscodeSetup) Validate(target QualityTarget) config.Problems {
	var problems config.Problems
	problem := func(path string, value any, expectation string) {
		problems = append(problems, config.Problem{Path: path, Value: fmt.Sprint(value), Expectation: expectation})
	}
	between := func(path string, value, low, high int64) {
		if value < low || value > high {
			problem(path, value, fmt.Sprintf("%d to %d", low, high))
		}
	}
	oneOf := func(path string, value string, allowed ...string) {
		if !slices.Contains(allowed, value) {
			problem(path, value, "one of "+strings.Join(allowed, ", "))
		}
	}
	if s.Version != TranscodeSetupVersion {
		problem("version", s.Version, "version "+strconv.Itoa(TranscodeSetupVersion))
	}
	oneOf("encode.engine", string(s.Encode.Engine), "auto", "software", "qsv", "vaapi", "nvenc")
	if target.isTranscodeVideo() {
		oneOf("encode.rate_mode", string(s.Encode.RateMode), "quality", "bitrate")
		oneOf("encode.preset", string(s.Encode.Preset), "fast", "normal", "slow", "slower", "veryslow")
		if s.Encode.Quality < 0 || s.Encode.Quality > 63 {
			problem("encode.quality", s.Encode.Quality, "0 to 63")
		}
		strengths := []string{"off", "low", "medium", "strong"}
		for path, value := range map[string]FilterStrength{
			"video_filters.denoise": s.Video.Denoise, "video_filters.deblock": s.Video.Deblock, "video_filters.deband": s.Video.Deband,
			"video_filters.deflicker": s.Video.Deflicker, "video_filters.sharpen": s.Video.Sharpen,
		} {
			oneOf(path, string(value), strengths...)
		}
		oneOf("video_filters.denoise_method", string(s.Video.DenoiseMethod), "auto", "hqdn3d", "nlmeans", "bm3d")
		oneOf("video_filters.sharpen_method", string(s.Video.SharpenMethod), "cas", "smartblur")
	}
	if s.Encode.VideoBitrateKbps < 0 {
		problem("encode.video_bitrate_kbps", s.Encode.VideoBitrateKbps, "0 or more")
	}
	between("skip.range_pct", s.Skip.RangePct, 1, 100)
	between("skip.max_pct", s.Skip.MaxPct, 1, 1000)
	between("streams.video_copy_max_pct", s.Streams.VideoCopyMaxPct, 1, 1000)
	between("streams.audio_copy_max_pct", s.Streams.AudioCopyMaxPct, 1, 1000)
	between("check.duration_seconds", s.Check.DurationSeconds, 0, 60)
	between("check.range_pct", s.Check.RangePct, 1, 100)
	between("check.max_pct", s.Check.MaxPct, 1, 1000)
	between("ladder.extra_passes", s.Ladder.ExtraPasses, 0, 4)
	between("ladder.bitrate_step_pct", s.Ladder.BitrateStepPct, 1, 90)
	if s.Ladder.QualityStep <= 0 {
		problem("ladder.quality_step", s.Ladder.QualityStep, "more than 0")
	}
	oneOf("audio_filters.denoise", string(s.Audio.Denoise), "off", "low", "medium", "strong")
	oneOf("audio_filters.denoise_method", string(s.Audio.DenoiseMethod), "afftdn", "anlmdn")
	return problems
}

// TranscodeWarningCode names a way a setup is likely to miss its target.
type TranscodeWarningCode string

// The warnings that follow from the setup and its target alone.
const (
	WarnQualityDiffersFromTarget TranscodeWarningCode = "quality_differs_from_target"
	WarnChecksOff                TranscodeWarningCode = "checks_off"
	WarnRangeUnknown             TranscodeWarningCode = "range_unknown"
	WarnBitrateOutsideRange      TranscodeWarningCode = "bitrate_outside_range"
	WarnResultsOutsideRange      TranscodeWarningCode = "results_outside_range"
)

// TranscodeWarning is one warning with the number that raised it, when there is one.
type TranscodeWarning struct {
	Code  TranscodeWarningCode
	Value string
}

// TranscodeWarnings lists how the setup is likely to miss its target.
func TranscodeWarnings(setup TranscodeSetup, target QualityTarget) []TranscodeWarning {
	warnings := []TranscodeWarning{}
	video := target.isTranscodeVideo()
	if video && setup.Encode.RateMode == RateQuality && target.CRF > 0 {
		if gap := setup.Encode.Quality - target.CRF; gap > 2 || gap < -2 {
			warnings = append(warnings, TranscodeWarning{Code: WarnQualityDiffersFromTarget, Value: strconv.FormatFloat(setup.Encode.Quality, 'f', -1, 64)})
		}
	}
	if !setup.Check.WithinRange && !setup.Check.Smaller {
		warnings = append(warnings, TranscodeWarning{Code: WarnChecksOff})
	}
	if (video && target.VideoBitrateKbps == nil) || (!video && target.BitrateKbps == nil) {
		warnings = append(warnings, TranscodeWarning{Code: WarnRangeUnknown})
	}
	if video && setup.Encode.RateMode == RateBitrate && setup.Encode.VideoBitrateKbps > 0 && target.VideoBitrateKbps != nil {
		low, high := bitrateRange(*target.VideoBitrateKbps, target.TolerancePct)
		if setup.Encode.VideoBitrateKbps < low || setup.Encode.VideoBitrateKbps > high {
			warnings = append(warnings, TranscodeWarning{Code: WarnBitrateOutsideRange, Value: strconv.FormatInt(setup.Encode.VideoBitrateKbps, 10)})
		}
	}
	return warnings
}

// MinFinishedForResultsWarning is how many converted files must exist before their median counts as evidence about the setup.
const MinFinishedForResultsWarning = 10

// TranscodeResultsWarning is the warning when the median of finished files' result-to-target bitrate ratios (percent) lies outside the target's range.
func TranscodeResultsWarning(ratiosPct []int64, target QualityTarget) (TranscodeWarning, bool) {
	if len(ratiosPct) < MinFinishedForResultsWarning {
		return TranscodeWarning{}, false
	}
	sorted := slices.Clone(ratiosPct)
	slices.Sort(sorted)
	median := sorted[len(sorted)/2]
	low, high := bitrateRange(100, target.TolerancePct)
	if median >= low && median <= high {
		return TranscodeWarning{}, false
	}
	return TranscodeWarning{Code: WarnResultsOutsideRange, Value: strconv.FormatInt(median, 10)}, true
}

// bitrateRange is the target bitrate widened by the tolerance each way.
func bitrateRange(target, tolerancePct int64) (low, high int64) {
	return target * (100 - tolerancePct) / 100, target * (100 + tolerancePct) / 100
}

// TranscodeKind says what a target converts to: video, audio, or other for what transcoding does not cover (books, comics).
func (t QualityTarget) TranscodeKind() string {
	switch {
	case t.isTranscodeVideo():
		return "video"
	case TargetFamily(t.MediaType) == "book":
		return "other"
	}
	return "audio"
}
