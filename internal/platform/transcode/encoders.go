package transcode

import (
	"context"
	"strings"
)

// Codec is a video codec a target can name.
type Codec string

// The video codecs a conversion can write.
const (
	CodecH264 Codec = "h264"
	CodecHEVC Codec = "hevc"
	CodecAV1  Codec = "av1"
	CodecVP9  Codec = "vp9"
)

// Codecs lists every codec in display order.
var Codecs = []Codec{CodecH264, CodecHEVC, CodecAV1, CodecVP9}

// CodecNamed maps a quality target's video codec name to the encoder matrix's; false when the target names none or one that is not encoded.
func CodecNamed(name string) (Codec, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "h264", "avc", "x264":
		return CodecH264, true
	case "h265", "hevc", "x265":
		return CodecHEVC, true
	case "av1":
		return CodecAV1, true
	case "vp9":
		return CodecVP9, true
	}
	return "", false
}

// Engines lists the encoding engines in display order.
var Engines = []Accel{Software, QSV, VAAPI, NVENC}

// EncoderSupport is whether one engine encodes one codec here, and with what.
type EncoderSupport struct {
	Engine  Accel
	Codec   Codec
	Encoder string
	Working bool
	// Detail is why not: the build lacks the encoder, or the driver's last line.
	Detail    string
	Lookahead bool
	TenBit    bool
	// QualityMode is the rate control the quality setting resolved to: crf, la_icq, icq, cqp or cq.
	QualityMode string
}

// noNVENC is the sentence shown for every NVENC row of a machine that cannot use it.
const noNVENC = "this ffmpeg build has no NVENC"

// encoderName is the ffmpeg encoder of an engine and codec; false when the engine has none for it.
func encoderName(engine Accel, codec Codec) (string, bool) {
	switch engine {
	case Software:
		name, ok := map[Codec]string{CodecH264: "libx264", CodecHEVC: "libx265", CodecAV1: "libsvtav1", CodecVP9: "libvpx-vp9"}[codec]
		return name, ok
	case QSV, VAAPI:
		if _, known := map[Codec]bool{CodecH264: true, CodecHEVC: true, CodecAV1: true, CodecVP9: true}[codec]; known {
			return string(codec) + "_" + string(engine), true
		}
	case NVENC:
		if codec != CodecVP9 {
			return string(codec) + "_nvenc", true
		}
	}
	return "", false
}

// listedNames reads the second column of `ffmpeg -encoders` or `-filters`, where the name is.
func listedNames(output []byte) map[string]bool {
	names := map[string]bool{}
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && len(fields[0]) >= 3 && !strings.HasSuffix(fields[0], ":") && !strings.Contains(fields[0], "=") {
			names[fields[1]] = true
		}
	}
	return names
}

func builtEncoders(ctx context.Context, runner Runner) map[string]bool {
	output, err := runner.Run(ctx, ffmpeg, "-hide_banner", "-encoders")
	if err != nil {
		return map[string]bool{}
	}
	return listedNames(output)
}

// wantedFilters are the filters the setup form may switch on; the map says which this build has.
var wantedFilters = []string{
	"bwdif", "deflicker", "hqdn3d", "nlmeans", "bm3d", "deblock", "deband", "zscale", "tonemap", "cas", "smartblur",
	"vpp_qsv", "deinterlace_vaapi", "denoise_vaapi", "tonemap_vaapi", "scale_vaapi", "bwdif_cuda", "bilateral_cuda", "scale_cuda",
	"adeclip", "afftdn", "anlmdn", "dialoguenhance", "acompressor", "loudnorm",
}

func builtFilters(ctx context.Context, runner Runner) map[string]bool {
	output, err := runner.Run(ctx, ffmpeg, "-hide_banner", "-filters")
	if err != nil {
		return map[string]bool{}
	}
	listed := listedNames(output)
	out := map[string]bool{}
	for _, name := range wantedFilters {
		out[name] = listed[name]
	}
	return out
}

func ffmpegVersion(ctx context.Context, runner Runner) string {
	output, err := runner.Run(ctx, ffmpeg, "-hide_banner", "-version")
	if err != nil {
		return ""
	}
	fields := strings.Fields(strings.SplitN(string(output), "\n", 2)[0])
	if len(fields) >= 3 && fields[1] == "version" {
		return fields[2]
	}
	return ""
}

// DetectEncoders answers, for every engine and codec, whether this machine encodes it: software by the build alone,
// a GPU engine by encoding one second of test video with the exact rate-control arguments a conversion uses.
func DetectEncoders(ctx context.Context, runner Runner, capabilities []Capability, built map[string]bool) []EncoderSupport {
	var out []EncoderSupport
	for _, engine := range Engines {
		for _, codec := range Codecs {
			encoder, known := encoderName(engine, codec)
			if !known {
				continue
			}
			row := EncoderSupport{Engine: engine, Codec: codec, Encoder: encoder}
			switch {
			case !built[encoder]:
				row.Detail = "this ffmpeg build has no " + encoder
				if engine == NVENC {
					row.Detail = noNVENC
				}
			case engine == Software:
				row.Working, row.Lookahead, row.QualityMode, row.TenBit = true, true, "crf", encoder != "libx264"
			default:
				row = detectGPU(ctx, runner, capabilities, row)
			}
			out = append(out, row)
		}
	}
	return out
}

// firstCapability is the working or, failing that, first capability of an engine.
func firstCapability(capabilities []Capability, engine Accel) (Capability, bool) {
	var first Capability
	found := false
	for _, capability := range capabilities {
		if capability.Accel != engine {
			continue
		}
		if capability.Working {
			return capability, true
		}
		if !found {
			first, found = capability, true
		}
	}
	return first, found
}

func detectGPU(ctx context.Context, runner Runner, capabilities []Capability, row EncoderSupport) EncoderSupport {
	capability, found := firstCapability(capabilities, row.Engine)
	if !found || !capability.Working {
		row.Detail = "no working " + string(row.Engine) + " device"
		if found && capability.Detail != "" {
			row.Detail = capability.Detail
		}
		if row.Engine == NVENC {
			row.Detail = noNVENC
		}
		return row
	}
	var lastFailure string
	for _, mode := range gpuQualityModes(row) {
		for _, lookahead := range []bool{true, false} {
			rate := RateControl{Mode: RateModeQuality, Quality: 25, Lookahead: lookahead, QualityMode: mode}
			if _, err := runner.Run(ctx, ffmpeg, testEncodeArgs(capability, row.Encoder, rate, false)...); err != nil {
				lastFailure = hint(lastLine(err.Error()))
				continue
			}
			row.Working, row.Lookahead, row.Detail = true, lookahead, ""
			row.QualityMode = resolvedQualityMode(row, mode, lookahead)
			_, tenBitErr := runner.Run(ctx, ffmpeg, testEncodeArgs(capability, row.Encoder, rate, true)...)
			row.TenBit = tenBitErr == nil
			return row
		}
	}
	row.Detail = lastFailure
	return row
}

// gpuQualityModes are the modes tried in order; VAAPI has two, everything else one.
func gpuQualityModes(row EncoderSupport) []string {
	if row.Engine == VAAPI {
		return []string{"icq", "cqp"}
	}
	return []string{""}
}

func resolvedQualityMode(row EncoderSupport, mode string, lookahead bool) string {
	switch {
	case row.Engine == NVENC:
		return "cq"
	case row.Engine == QSV && row.Codec == CodecH264 && lookahead:
		return "la_icq"
	case row.Engine == QSV:
		return "icq"
	}
	return mode
}

// testEncodeArgs encodes one second of test video with the candidate and throws it away.
func testEncodeArgs(capability Capability, encoder string, rate RateControl, tenBit bool) []string {
	source, filter := hardwareInput(capability, tenBit)
	source = append(source, "-f", "lavfi", "-i", "testsrc=size=320x240:rate=25:duration=1", "-vf", filter, "-c:v", encoder)
	source = append(source, RateArgs(encoder, rate)...)
	return append(source, "-f", "null", "-")
}

// DeviceFor is the render device a working detection of the engine used, empty for software and for an engine without one.
func (s *Service) DeviceFor(engine Accel) string {
	for _, capability := range s.Capabilities() {
		if capability.Accel == engine && capability.Working {
			return capability.Device
		}
	}
	return ""
}
