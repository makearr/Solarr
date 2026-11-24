package transcode_test

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/platform/transcode"
)

// matrixRunner answers the listing commands from fixed text and fails any test encode whose arguments contain a refused word.
type matrixRunner struct {
	encoders string
	refuse   []string
}

func (matrixRunner) Lookup(string) error { return nil }

func (r matrixRunner) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	switch {
	case slices.Contains(args, "-encoders"):
		return []byte(r.encoders), nil
	case slices.Contains(args, "-filters"):
		return []byte(" T.. bwdif             V->V       Deinterlace\n ... loudnorm          A->A       EBU R128\n"), nil
	case slices.Contains(args, "-version"):
		return []byte("ffmpeg version 8.0.1 Copyright (c) 2000-2025\n"), nil
	case slices.Contains(args, "-hwaccels"):
		return nil, nil
	}
	joined := strings.Join(args, " ")
	for _, word := range r.refuse {
		if strings.Contains(joined, word) {
			return nil, errors.New("setup\nrefused: " + word)
		}
	}
	return nil, nil
}

const listedEncoders = `Encoders:
 V....D libx264              libx264 H.264
 V....D libx265              libx265 H.265
 V....D hevc_qsv             HEVC (Intel Quick Sync)
 V....D h264_qsv             H.264 (Intel Quick Sync)
 V....D hevc_vaapi           H.265/HEVC (VAAPI)
 V....D av1_vaapi            AV1 (VAAPI)
`

var gpus = []transcode.Capability{
	{Accel: transcode.QSV, Device: "/dev/dri/renderD128", Name: "Intel", Working: true},
	{Accel: transcode.VAAPI, Device: "/dev/dri/renderD128", Name: "Intel", Working: true},
}

func rowOf(t *testing.T, rows []transcode.EncoderSupport, engine transcode.Accel, codec transcode.Codec) transcode.EncoderSupport {
	t.Helper()
	for _, row := range rows {
		if row.Engine == engine && row.Codec == codec {
			return row
		}
	}
	t.Fatalf("no row for %s %s", engine, codec)
	return transcode.EncoderSupport{}
}

func TestDetectEncoders_SoftwareRowsFollowTheBuildAndNVENCSaysItIsMissing(t *testing.T) {
	runner := matrixRunner{encoders: listedEncoders}
	rows := transcode.DetectEncoders(t.Context(), runner, nil, map[string]bool{"libx264": true, "libx265": true})
	if got, want := rowOf(t, rows, transcode.Software, transcode.CodecHEVC), (transcode.EncoderSupport{
		Engine: transcode.Software, Codec: transcode.CodecHEVC, Encoder: "libx265", Working: true, Lookahead: true, TenBit: true, QualityMode: "crf",
	}); got != want {
		t.Errorf("x265 = %+v, want %+v", got, want)
	}
	if got := rowOf(t, rows, transcode.Software, transcode.CodecH264); got.TenBit || !got.Working {
		t.Errorf("x264 = %+v: working, never 10-bit", got)
	}
	if got := rowOf(t, rows, transcode.Software, transcode.CodecAV1); got.Working || got.Detail != "this ffmpeg build has no libsvtav1" {
		t.Errorf("av1 = %+v", got)
	}
	for _, codec := range []transcode.Codec{transcode.CodecH264, transcode.CodecHEVC, transcode.CodecAV1} {
		if got := rowOf(t, rows, transcode.NVENC, codec); got.Working || got.Detail != "this ffmpeg build has no NVENC" {
			t.Errorf("nvenc %s = %+v", codec, got)
		}
	}
	if got := rowOf(t, rows, transcode.QSV, transcode.CodecHEVC); got.Working || got.Detail != "this ffmpeg build has no hevc_qsv" {
		t.Errorf("qsv without a device or build = %+v", got)
	}
}

func TestDetectEncoders_AGPUWithoutADeviceIsGreyedWithItsReason(t *testing.T) {
	built := map[string]bool{"hevc_vaapi": true}
	found := []transcode.Capability{{Accel: transcode.VAAPI, Name: "GPU", Detail: "no render device: start the container with --device /dev/dri"}}
	rows := transcode.DetectEncoders(t.Context(), matrixRunner{}, found, built)
	if got := rowOf(t, rows, transcode.VAAPI, transcode.CodecHEVC); got.Working || !strings.Contains(got.Detail, "--device /dev/dri") {
		t.Errorf("vaapi = %+v", got)
	}
}

func TestDetectEncoders_LookaheadRefusedIsRetriedWithoutAndRecorded(t *testing.T) {
	built := map[string]bool{"hevc_qsv": true, "h264_qsv": true}
	rows := transcode.DetectEncoders(t.Context(), matrixRunner{refuse: []string{"-extbrc"}}, gpus, built)
	if got := rowOf(t, rows, transcode.QSV, transcode.CodecHEVC); !got.Working || got.Lookahead || got.QualityMode != "icq" || !got.TenBit {
		t.Errorf("hevc_qsv = %+v: works without lookahead", got)
	}
	if got := rowOf(t, rows, transcode.QSV, transcode.CodecH264); !got.Working || !got.Lookahead || got.QualityMode != "la_icq" {
		t.Errorf("h264_qsv = %+v: lookahead accepted", got)
	}
}

func TestDetectEncoders_VAAPIFallsToCQPAndTenBitIsRecordedSeparately(t *testing.T) {
	built := map[string]bool{"hevc_vaapi": true}
	rows := transcode.DetectEncoders(t.Context(), matrixRunner{refuse: []string{"ICQ", "p010"}}, gpus, built)
	if got := rowOf(t, rows, transcode.VAAPI, transcode.CodecHEVC); !got.Working || got.QualityMode != "cqp" || got.TenBit {
		t.Errorf("hevc_vaapi = %+v: CQP, 8-bit only", got)
	}
	refusedAll := transcode.DetectEncoders(t.Context(), matrixRunner{refuse: []string{"hevc_vaapi"}}, gpus, built)
	if got := rowOf(t, refusedAll, transcode.VAAPI, transcode.CodecHEVC); got.Working || got.Detail != "this VAAPI device cannot encode hevc" {
		t.Errorf("a refused encoder = %+v", got)
	}
}

func TestService_BestEngineTakesNVENCThenQSVThenVAAPIAndNeedsTenBitWhenAsked(t *testing.T) {
	service := transcode.NewService(matrixRunner{encoders: listedEncoders, refuse: []string{"p010,hwupload=extra"}}, nil, memWithNode(t, true))
	service.Detect(t.Context())
	service.DetectEncoders(t.Context())
	if engine, ok := service.BestEngine(transcode.CodecHEVC, false); !ok || engine != transcode.QSV {
		t.Errorf("best hevc = %s %v, want qsv", engine, ok)
	}
	if engine, ok := service.BestEngine(transcode.CodecHEVC, true); !ok || engine != transcode.VAAPI {
		t.Errorf("best 10-bit hevc = %s %v, want vaapi (qsv refused p010)", engine, ok)
	}
	if _, ok := service.BestEngine(transcode.CodecVP9, false); ok {
		t.Error("vp9 has no GPU encoder here")
	}
	if got := service.FFmpegVersion(); got != "8.0.1" {
		t.Errorf("version = %q", got)
	}
	if filters := service.Filters(); !filters["bwdif"] || filters["hqdn3d"] {
		t.Errorf("filters = %v", filters)
	}
}

func TestRateArgs_WholeArgumentListsPerEncoderAndModeWithAndWithoutLookahead(t *testing.T) {
	quality := func(lookahead bool) transcode.RateControl {
		return transcode.RateControl{Mode: transcode.RateModeQuality, Quality: 28, Lookahead: lookahead}
	}
	bitrate := transcode.RateControl{Mode: transcode.RateModeBitrate, BitrateKbps: 2000, Lookahead: true}
	cases := []struct {
		name    string
		encoder string
		rate    transcode.RateControl
		want    []string
	}{
		{"x265 quality", "libx265", quality(true), []string{"-crf", "28", "-x265-params", "rc-lookahead=40:log-level=error"}},
		{"x265 without lookahead", "libx265", quality(false), []string{"-crf", "28", "-x265-params", "log-level=error"}},
		{"x264 without lookahead", "libx264", quality(false), []string{"-crf", "28"}},
		{"x264 bitrate", "libx264", bitrate, []string{"-b:v", "2000k", "-maxrate", "3000k", "-bufsize", "6000k", "-x264-params", "rc-lookahead=40"}},
		{"svtav1 quality", "libsvtav1", quality(true), []string{"-crf", "28", "-svtav1-params", "lookahead=120"}},
		{"vp9 quality", "libvpx-vp9", quality(true), []string{"-crf", "28", "-b:v", "0", "-lag-in-frames", "25", "-row-mt", "1"}},
		{"h264 qsv", "h264_qsv", quality(true), []string{"-global_quality", "28", "-look_ahead", "1", "-look_ahead_depth", "40"}},
		{"hevc qsv", "hevc_qsv", quality(true), []string{"-global_quality", "28", "-extbrc", "1", "-look_ahead_depth", "40"}},
		{"hevc qsv without lookahead", "hevc_qsv", quality(false), []string{"-global_quality", "28"}},
		{"vaapi icq", "hevc_vaapi", quality(true), []string{"-rc_mode", "ICQ", "-global_quality", "28"}},
		{"vaapi cqp", "hevc_vaapi", transcode.RateControl{Mode: transcode.RateModeQuality, Quality: 28, QualityMode: "cqp"}, []string{"-rc_mode", "CQP", "-qp", "28"}},
		{"vaapi bitrate", "av1_vaapi", bitrate, []string{"-rc_mode", "VBR", "-b:v", "2000k", "-maxrate", "3000k"}},
		{"nvenc quality", "hevc_nvenc", quality(true), []string{"-rc", "vbr", "-cq", "28", "-b:v", "0", "-rc-lookahead", "32", "-spatial_aq", "1"}},
		{"nvenc bitrate", "h264_nvenc", bitrate, []string{"-rc", "vbr", "-b:v", "2000k", "-maxrate", "3000k", "-rc-lookahead", "32"}},
	}
	for _, c := range cases {
		if got := transcode.RateArgs(c.encoder, c.rate); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s = %v, want %v", c.name, got, c.want)
		}
	}
}

type execRunner struct{}

func (execRunner) Lookup(name string) error { _, err := exec.LookPath(name); return err }

func (execRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return nil, errors.New(string(output))
	}
	return output, nil
}

func TestDetectEncoders_RealFFmpegEncodesWithTheSoftwareEncodersItHas(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not on PATH")
	}
	service := transcode.NewService(execRunner{}, nil, memWithNode(t, false))
	rows := service.DetectEncoders(t.Context())
	built := false
	for _, row := range rows {
		if row.Engine == transcode.Software && row.Working {
			built = true
		}
	}
	if !built {
		t.Skip("this ffmpeg has no software video encoder")
	}
	if service.FFmpegVersion() == "" {
		t.Error("no ffmpeg version read")
	}
}

func TestProbeStreams_RealFFmpegFileWithTwoAudioLanguagesProbesBack(t *testing.T) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe is not on PATH")
	}
	path := t.TempDir() + "/two.mkv"
	command := exec.CommandContext(t.Context(), "ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "sine=duration=1", "-f", "lavfi", "-i", "sine=frequency=880:duration=1",
		"-map", "0:a", "-map", "1:a", "-c:a", "aac", "-metadata:s:a:0", "language=eng", "-metadata:s:a:1", "language=deu", path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Skipf("cannot generate the file: %v: %s", err, output)
	}
	file, err := transcode.NewService(execRunner{}, nil, nil).ProbeStreams(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Streams) != 2 || file.Streams[0].Language != "en" || file.Streams[1].Language != "de" || file.Streams[0].Type != transcode.StreamAudio || file.Streams[0].Channels != 1 {
		t.Errorf("streams = %+v", file.Streams)
	}
}
