package transcode

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// ConvertSpec is one conversion of one file, fully decided; ConvertArgs only spells it.
type ConvertSpec struct {
	Input, Output string
	// Kind is video for a file with pictures and audio for a music file; it picks the loudness target.
	Kind      Kind
	Container string
	Engine    Accel
	Device    string
	// FullGPU keeps frames on the GPU from decoding to encoding; false decodes on the CPU and uploads.
	FullGPU bool
	// Tolerant makes the input reader forgive damage (attempt 2 and later).
	Tolerant  bool
	Video     []VideoOut
	Audio     []AudioOut
	Subtitles []SubtitleOut
	// Covers are the input streams kept as attached pictures; KeepFonts the attachments kept beside ASS subtitles.
	Covers    []int
	KeepFonts []int
	// Tags is the complete set written after the source's own are dropped.
	Tags   map[string]string
	Marker string
	// CustomTags makes MP4 keep tags that have no iTunes atom (MusicBrainz ids, the marker); ffmpeg then loses a cover picture.
	CustomTags bool
}

// VideoOut is one video stream of the output.
type VideoOut struct {
	Index int
	Copy  bool
	// Encoder is the ffmpeg encoder of an encoded stream.
	Encoder string
	Rate    RateControl
	// Preset is fast, normal, slow, slower or veryslow.
	Preset string
	TenBit bool
	// Width and Height are set only when the size changes; FPS only when the rate changes.
	Width, Height int
	FPS           float64
	Filters       []VideoFilter
	// HDR is keep, tonemap or empty; keep passes the source's colour tags to the encoder.
	HDR                                       string
	ColorPrimaries, ColorTransfer, ColorSpace string
}

// AudioFilterKind names one audio filter a conversion can apply.
type AudioFilterKind string

// The audio filters, in the order the chain applies them.
const (
	AudioDeclip        AudioFilterKind = "declip"
	AudioDenoiseFFT    AudioFilterKind = "afftdn"
	AudioDenoiseNLM    AudioFilterKind = "anlmdn"
	AudioDialogEnhance AudioFilterKind = "dialoguenhance"
	AudioCompress      AudioFilterKind = "compress"
	AudioNormalize     AudioFilterKind = "loudnorm"
)

// AudioFilter is one audio filter; only the denoisers have a strength.
type AudioFilter struct {
	Kind     AudioFilterKind
	Strength string
}

// AudioOut is one audio stream of the output.
type AudioOut struct {
	Index       int
	Copy        bool
	Encoder     string
	BitrateKbps int64
	// Channels and SampleRate are always the output's; loudnorm needs SampleRate because it would answer 192 kHz.
	Channels, SampleRate int
	Filters              []AudioFilter
	Language, Title      string
	Default              bool
}

// SubtitleOut is one subtitle stream of the output.
type SubtitleOut struct {
	Index           int
	Copy            bool
	Codec           string
	Language, Title string
	Default, Forced bool
}

// onGPU says a GPU engine encodes the video; no engine means the CPU.
func (s ConvertSpec) onGPU() bool { return slices.Contains([]Accel{QSV, VAAPI, NVENC}, s.Engine) }

// AudioEncoder is the ffmpeg encoder of an audio codec; unknown codecs are their own name.
func AudioEncoder(codec string) string {
	switch codec {
	case "opus":
		return "libopus"
	case "mp3":
		return "libmp3lame"
	case "vorbis":
		return "libvorbis"
	}
	return codec
}

type containerFlags struct {
	muxer    string
	movflags bool
}

var containers = map[string]containerFlags{
	"mp4": {"mp4", true}, "m4v": {"mp4", true}, "mkv": {"matroska", false}, "matroska": {"matroska", false},
	"m4a": {"ipod", true}, "webm": {"webm", false}, "flac": {"flac", false}, "mp3": {"mp3", false},
	"opus": {"opus", false}, "ogg": {"ogg", false},
}

var (
	audioDenoiseFFT = [3]string{"afftdn=nr=6", "afftdn=nr=12", "afftdn=nr=20"}
	audioDenoiseNLM = [3]string{"anlmdn=s=3", "anlmdn=s=7", "anlmdn=s=12"}
)

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

var errNoStreams = errors.New("transcode: the conversion has no stream to write")

// ConvertArgs builds the ffmpeg command line of a conversion; it never uses -pass, so video is one run.
func ConvertArgs(spec ConvertSpec) ([]string, error) {
	flags, known := containers[strings.ToLower(spec.Container)]
	switch {
	case !known:
		return nil, fmt.Errorf("transcode: unknown container %q", spec.Container)
	case len(spec.Video) == 0 && len(spec.Audio) == 0:
		return nil, errNoStreams
	}
	args := []string{"-hide_banner", "-nostdin", "-loglevel", "error", "-nostats", "-progress", "pipe:1", "-y"}
	if spec.Tolerant {
		args = append(args, "-err_detect", "ignore_err", "-fflags", "+igndts+discardcorrupt", "-max_error_rate", "1.0")
	}
	if spec.onGPU() && slices.ContainsFunc(spec.Video, func(v VideoOut) bool { return !v.Copy }) {
		args = append(args, hardwareArgs(spec.Engine, spec.Device, spec.FullGPU)...)
	}
	args = append(args, "-protocol_whitelist", "file", "-i", spec.Input)
	args = append(args, mapArgs(spec)...)
	args = append(args, "-map_metadata", "-1", "-map_chapters", "0")

	outVideo := 0
	for _, video := range spec.Video {
		streamArgs, err := videoArgs(spec, video, outVideo, flags)
		if err != nil {
			return nil, err
		}
		args = append(args, streamArgs...)
		outVideo++
	}
	for range spec.Covers {
		args = append(args, "-c:v:"+strconv.Itoa(outVideo), "copy", "-disposition:v:"+strconv.Itoa(outVideo), "attached_pic")
		outVideo++
	}
	for position, audio := range spec.Audio {
		streamArgs, err := audioArgs(spec, audio, position)
		if err != nil {
			return nil, err
		}
		args = append(args, streamArgs...)
	}
	for position, subtitle := range spec.Subtitles {
		args = append(args, subtitleArgs(subtitle, position)...)
	}
	if len(spec.KeepFonts) > 0 {
		args = append(args, "-c:t", "copy")
	}
	keys := make([]string, 0, len(spec.Tags))
	for key := range spec.Tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, "-metadata", key+"="+spec.Tags[key])
	}
	if spec.Marker != "" {
		args = append(args, "-metadata", MarkerKey+"="+spec.Marker)
	}
	if flags.movflags {
		movflags := "+faststart"
		if spec.CustomTags {
			movflags += "+use_metadata_tags"
		}
		args = append(args, "-movflags", movflags)
	}
	return append(args, "-max_muxing_queue_size", "4096", "-f", flags.muxer, spec.Output), nil
}

// mapArgs maps exactly the streams the spec names, in the order the per-stream arguments count them.
func mapArgs(spec ConvertSpec) []string {
	var args []string
	add := func(index int) { args = append(args, "-map", "0:"+strconv.Itoa(index)) }
	for _, video := range spec.Video {
		add(video.Index)
	}
	for _, audio := range spec.Audio {
		add(audio.Index)
	}
	for _, subtitle := range spec.Subtitles {
		add(subtitle.Index)
	}
	// Covers follow the audio: MP4 keeps a leading picture as a tag that ffprobe does not list.
	for _, index := range spec.Covers {
		add(index)
	}
	for _, index := range spec.KeepFonts {
		add(index)
	}
	return args
}

func videoArgs(spec ConvertSpec, video VideoOut, position int, flags containerFlags) ([]string, error) {
	suffix := ":v:" + strconv.Itoa(position)
	if video.Copy {
		return []string{"-c" + suffix, "copy"}, nil
	}
	if video.Encoder == "" {
		return nil, fmt.Errorf("transcode: video stream %d is encoded without an encoder", video.Index)
	}
	args := []string{"-c" + suffix, video.Encoder}
	args = append(args, presetArgs(video.Encoder, video.Preset)...)
	args = append(args, RateArgs(video.Encoder, video.Rate)...)
	if chain := videoChain(spec, video); chain != "" {
		args = append(args, "-filter"+suffix, chain)
	}
	if video.HDR == HDRKeep {
		for _, tag := range [][2]string{{"-color_primaries", video.ColorPrimaries}, {"-color_trc", video.ColorTransfer}, {"-colorspace", video.ColorSpace}} {
			if tag[1] != "" {
				args = append(args, tag[0]+suffix, tag[1])
			}
		}
	}
	if strings.HasPrefix(video.Encoder, "hevc") || video.Encoder == "libx265" {
		if flags.muxer == "mp4" {
			args = append(args, "-tag"+suffix, "hvc1")
		}
	}
	return args, nil
}

// videoChain is the -filter chain of an encoded stream for the engine and path in the spec.
func videoChain(spec ConvertSpec, video VideoOut) string {
	if !spec.onGPU() {
		return softwareChain(video)
	}
	if spec.FullGPU {
		chain, _ := gpuChain(spec.Engine, video)
		return chain
	}
	return uploadChain(spec.Engine, video)
}

var presetNames = map[string]string{"fast": "fast", "normal": "medium", "slow": "slow", "slower": "slower", "veryslow": "veryslow"}

var (
	svtPreset    = map[string]string{"fast": "8", "normal": "6", "slow": "5", "slower": "4", "veryslow": "3"}
	vp9CPUUsed   = map[string]string{"fast": "4", "normal": "3", "slow": "2", "slower": "1", "veryslow": "0"}
	nvencPresets = map[string]string{"fast": "p3", "normal": "p4", "slow": "p5", "slower": "p6", "veryslow": "p7"}
)

// presetArgs are the speed arguments of an encoder for a setup preset; VAAPI has none.
func presetArgs(encoder, preset string) []string {
	switch {
	case encoder == "libx264", encoder == "libx265", strings.HasSuffix(encoder, "_qsv"):
		if name, ok := presetNames[preset]; ok {
			return []string{"-preset", name}
		}
	case encoder == "libsvtav1":
		if value, ok := svtPreset[preset]; ok {
			return []string{"-preset", value}
		}
	case encoder == "libvpx-vp9":
		if value, ok := vp9CPUUsed[preset]; ok {
			return []string{"-deadline", "good", "-cpu-used", value}
		}
	case strings.HasSuffix(encoder, "_nvenc"):
		if value, ok := nvencPresets[preset]; ok {
			return []string{"-preset", value}
		}
	}
	return nil
}

func audioArgs(spec ConvertSpec, audio AudioOut, position int) ([]string, error) {
	suffix := ":a:" + strconv.Itoa(position)
	args := []string{"-c" + suffix}
	if audio.Copy {
		args = append(args, "copy")
	} else {
		if audio.Encoder == "" {
			return nil, fmt.Errorf("transcode: audio stream %d is encoded without an encoder", audio.Index)
		}
		args = append(args, audio.Encoder)
		if audio.Encoder == "libopus" {
			args = append(args, "-vbr", "on")
		}
		if audio.BitrateKbps > 0 {
			args = append(args, "-b"+suffix, itoa(audio.BitrateKbps)+"k")
		}
		if audio.Channels > 0 {
			args = append(args, "-ac"+suffix, strconv.Itoa(audio.Channels))
		}
		normalising := slices.ContainsFunc(audio.Filters, func(f AudioFilter) bool { return f.Kind == AudioNormalize })
		if normalising && audio.SampleRate == 0 {
			return nil, fmt.Errorf("transcode: audio stream %d is normalised without a sample rate", audio.Index)
		}
		if audio.SampleRate > 0 {
			args = append(args, "-ar"+suffix, strconv.Itoa(audio.SampleRate))
		}
		if chain := audioChain(spec.Kind, audio); chain != "" {
			args = append(args, "-filter"+suffix, chain)
		}
	}
	args = append(args, languageArgs("s"+suffix, audio.Language, audio.Title)...)
	return append(args, "-disposition"+suffix, dispositionOf(audio.Default, false)), nil
}

// audioChain is the filter chain in the fixed order, ending in the layout libopus accepts.
func audioChain(kind Kind, audio AudioOut) string {
	var parts []string
	has := func(kinds ...AudioFilterKind) (AudioFilter, bool) {
		for _, filter := range audio.Filters {
			if slices.Contains(kinds, filter.Kind) {
				return filter, true
			}
		}
		return AudioFilter{}, false
	}
	if _, on := has(AudioDeclip); on {
		parts = append(parts, "adeclip")
	}
	if filter, on := has(AudioDenoiseFFT, AudioDenoiseNLM); on {
		table := audioDenoiseFFT
		if filter.Kind == AudioDenoiseNLM {
			table = audioDenoiseNLM
		}
		parts = append(parts, table[strengthIndex(filter.Strength)])
	}
	if _, on := has(AudioDialogEnhance); on {
		parts = append(parts, "dialoguenhance")
	}
	if _, on := has(AudioCompress); on {
		parts = append(parts, "acompressor=threshold=-18dB:ratio=3:attack=20:release=250")
	}
	if _, on := has(AudioNormalize); on {
		if kind == Audio {
			parts = append(parts, "loudnorm=I=-14:TP=-1.5:LRA=8")
		} else {
			parts = append(parts, "loudnorm=I=-16:TP=-1.5:LRA=11")
		}
	}
	if audio.Encoder == "libopus" && audio.Channels == 6 {
		parts = append(parts, "aformat=channel_layouts=5.1")
	}
	return strings.Join(parts, ",")
}

func subtitleArgs(subtitle SubtitleOut, position int) []string {
	suffix := ":s:" + strconv.Itoa(position)
	codec := subtitle.Codec
	if subtitle.Copy || codec == "" {
		codec = "copy"
	}
	args := []string{"-c" + suffix, codec}
	args = append(args, languageArgs("s"+suffix, subtitle.Language, subtitle.Title)...)
	return append(args, "-disposition"+suffix, dispositionOf(subtitle.Default, subtitle.Forced))
}

func languageArgs(spec, language, title string) []string {
	var args []string
	if language != "" {
		args = append(args, "-metadata:"+spec, "language="+mediatool.LanguageCode3(language))
	}
	if title != "" {
		args = append(args, "-metadata:"+spec, "title="+title)
	}
	return args
}

func dispositionOf(isDefault, forced bool) string {
	switch {
	case isDefault && forced:
		return "default+forced"
	case isDefault:
		return "default"
	case forced:
		return "forced"
	}
	return "0"
}
