package transcode_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/platform/transcode"
)

// build spells a spec and checks the one rule that holds for every output: video is a single run, never two passes.
func build(t *testing.T, spec transcode.ConvertSpec) []string {
	t.Helper()
	args, err := transcode.ConvertArgs(spec)
	if err != nil {
		t.Fatalf("ConvertArgs: %v", err)
	}
	if slices.Contains(args, "-pass") {
		t.Fatalf("-pass in %v", args)
	}
	return args
}

func assertArgs(t *testing.T, got []string, want string) {
	t.Helper()
	if joined := strings.Join(got, " "); joined != want {
		t.Fatalf("args:\n got  %s\n want %s", joined, want)
	}
}

const head = "-hide_banner -nostdin -loglevel error -nostats -progress pipe:1 -y"

func quality(q float64) transcode.RateControl {
	return transcode.RateControl{Mode: transcode.RateModeQuality, Quality: q, Lookahead: true}
}

func TestConvertArgs_SoftwareHEVCQualityIntoMP4(t *testing.T) {
	args := build(t, transcode.ConvertSpec{
		Input: "/in.mkv", Output: "/out.mp4", Kind: transcode.Video, Container: "mp4", Engine: transcode.Software,
		Video:     []transcode.VideoOut{{Index: 0, Encoder: "libx265", Rate: quality(28), Preset: "normal", TenBit: true}},
		Audio:     []transcode.AudioOut{{Index: 1, Encoder: "aac", BitrateKbps: 160, Channels: 2, SampleRate: 48000, Language: "en", Title: "Main", Default: true}},
		Subtitles: []transcode.SubtitleOut{{Index: 2, Codec: "mov_text", Language: "de", Forced: true}},
		Tags:      map[string]string{"title": "Pilot", "show": "Example"}, Marker: "Example;3;1;2026-11-24", CustomTags: true,
	})
	assertArgs(t, args, head+" -protocol_whitelist file -i /in.mkv -map 0:0 -map 0:1 -map 0:2 -map_metadata -1 -map_chapters 0"+
		" -c:v:0 libx265 -preset medium -crf 28 -x265-params rc-lookahead=40:log-level=error -filter:v:0 format=yuv420p10le -tag:v:0 hvc1"+
		" -c:a:0 aac -b:a:0 160k -ac:a:0 2 -ar:a:0 48000 -metadata:s:a:0 language=eng -metadata:s:a:0 title=Main -disposition:a:0 default"+
		" -c:s:0 mov_text -metadata:s:s:0 language=deu -disposition:s:0 forced"+
		" -metadata show=Example -metadata title=Pilot -metadata SOLARR_TRANSCODED=Example;3;1;2026-11-24"+
		" -movflags +faststart+use_metadata_tags -max_muxing_queue_size 4096 -f mp4 /out.mp4")
}

func TestConvertArgs_SoftwareBitrateAndOtherEncoders(t *testing.T) {
	cases := []struct {
		name  string
		video transcode.VideoOut
		want  string
	}{
		{"x264 bitrate", transcode.VideoOut{Encoder: "libx264", Preset: "slow", Rate: transcode.RateControl{Mode: transcode.RateModeBitrate, BitrateKbps: 4000, Lookahead: true}},
			"-c:v:0 libx264 -preset slow -b:v 4000k -maxrate 6000k -bufsize 12000k -x264-params rc-lookahead=40 -filter:v:0 format=yuv420p"},
		{"svt-av1", transcode.VideoOut{Encoder: "libsvtav1", Preset: "fast", Rate: quality(34)},
			"-c:v:0 libsvtav1 -preset 8 -crf 34 -svtav1-params lookahead=120 -filter:v:0 format=yuv420p"},
		{"vp9", transcode.VideoOut{Encoder: "libvpx-vp9", Preset: "veryslow", Rate: quality(31)},
			"-c:v:0 libvpx-vp9 -deadline good -cpu-used 0 -crf 31 -b:v 0 -lag-in-frames 25 -row-mt 1 -filter:v:0 format=yuv420p"},
		{"x264 without lookahead", transcode.VideoOut{Encoder: "libx264", Preset: "normal", Rate: transcode.RateControl{Mode: transcode.RateModeQuality, Quality: 23}},
			"-c:v:0 libx264 -preset medium -crf 23 -filter:v:0 format=yuv420p"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			args := build(t, transcode.ConvertSpec{Input: "/in.mkv", Output: "/out.mkv", Container: "mkv", Video: []transcode.VideoOut{c.video}})
			assertArgs(t, args, head+" -protocol_whitelist file -i /in.mkv -map 0:0 -map_metadata -1 -map_chapters 0 "+c.want+" -max_muxing_queue_size 4096 -f matroska /out.mkv")
		})
	}
}

func TestConvertArgs_SoftwareChainWithEveryFilter(t *testing.T) {
	video := transcode.VideoOut{
		Index: 0, Encoder: "libx264", Preset: "normal", Rate: quality(23), Width: 1280, Height: 720, FPS: 24, HDR: transcode.HDRTonemap,
		Filters: []transcode.VideoFilter{
			{Kind: transcode.FilterSmartBlur, Strength: "medium"}, {Kind: transcode.FilterDeband, Strength: "medium"},
			{Kind: transcode.FilterDeblock, Strength: "medium"}, {Kind: transcode.FilterHQDN3D, Strength: "medium"},
			{Kind: transcode.FilterDeflicker, Strength: "medium"}, {Kind: transcode.FilterDeinterlace},
		},
	}
	args := build(t, transcode.ConvertSpec{Input: "/in.mkv", Output: "/out.mkv", Container: "mkv", Video: []transcode.VideoOut{video}})
	assertArgs(t, args, head+" -protocol_whitelist file -i /in.mkv -map 0:0 -map_metadata -1 -map_chapters 0 -c:v:0 libx264 -preset medium -crf 23 -x264-params rc-lookahead=40"+
		" -filter:v:0 bwdif=mode=send_frame,deflicker=size=7,hqdn3d=4:3:6:4.5,deblock=filter=strong:block=4,deband=1thr=0.02:2thr=0.02:3thr=0.02,"+
		"zscale=t=linear:npl=100,format=gbrpf32le,zscale=p=bt709,tonemap=tonemap=hable:desat=0,zscale=t=bt709:m=bt709:r=tv,"+
		"scale=1280:-2:flags=lanczos,fps=24,smartblur=lr=1:ls=-0.6,format=yuv420p -max_muxing_queue_size 4096 -f matroska /out.mkv")
}

func TestConvertArgs_QSVFullGPUWithScaleAndFPS(t *testing.T) {
	video := transcode.VideoOut{Index: 0, Encoder: "hevc_qsv", Preset: "normal", Rate: quality(30), Width: 1280, Height: 720, FPS: 30, TenBit: true,
		Filters: []transcode.VideoFilter{{Kind: transcode.FilterHQDN3D, Strength: "low"}}}
	if !transcode.CanRunOnGPU(transcode.QSV, video) {
		t.Fatal("scale, fps and denoise have a QSV form")
	}
	args := build(t, transcode.ConvertSpec{Input: "/in.mkv", Output: "/out.mkv", Container: "mkv", Engine: transcode.QSV, Device: "/dev/dri/renderD128", FullGPU: true, Video: []transcode.VideoOut{video}})
	assertArgs(t, args, head+" -init_hw_device vaapi=va:/dev/dri/renderD128 -init_hw_device qsv=hw@va -hwaccel qsv -hwaccel_device hw -hwaccel_output_format qsv"+
		" -protocol_whitelist file -i /in.mkv -map 0:0 -map_metadata -1 -map_chapters 0 -c:v:0 hevc_qsv -preset medium -global_quality 30 -extbrc 1 -look_ahead_depth 40"+
		" -filter:v:0 vpp_qsv=denoise=20:w=1280:h=720:framerate=30:format=p010 -max_muxing_queue_size 4096 -f matroska /out.mkv")
}

func TestConvertArgs_GPUUploadOnlyAndOtherEngines(t *testing.T) {
	cases := []struct {
		name   string
		engine transcode.Accel
		full   bool
		video  transcode.VideoOut
		want   string
	}{
		{"qsv upload", transcode.QSV, false, transcode.VideoOut{Encoder: "h264_qsv", Preset: "slow", Rate: quality(24)},
			"-init_hw_device vaapi=va:/dev/dri/d -init_hw_device qsv=hw@va -filter_hw_device hw -protocol_whitelist file -i /in.mkv -map 0:0 -map_metadata -1 -map_chapters 0" +
				" -c:v:0 h264_qsv -preset slow -global_quality 24 -look_ahead 1 -look_ahead_depth 40 -filter:v:0 format=nv12,hwupload=extra_hw_frames=64"},
		{"vaapi icq upload", transcode.VAAPI, false, transcode.VideoOut{Encoder: "hevc_vaapi", Rate: quality(27), TenBit: true, Filters: []transcode.VideoFilter{{Kind: transcode.FilterCAS, Strength: "low"}}},
			"-init_hw_device vaapi=hw:/dev/dri/d -filter_hw_device hw -protocol_whitelist file -i /in.mkv -map 0:0 -map_metadata -1 -map_chapters 0" +
				" -c:v:0 hevc_vaapi -rc_mode ICQ -global_quality 27 -filter:v:0 cas=strength=0.3,format=p010,hwupload"},
		{"vaapi cqp full", transcode.VAAPI, true, transcode.VideoOut{Encoder: "hevc_vaapi", Rate: transcode.RateControl{Mode: transcode.RateModeQuality, Quality: 25, QualityMode: "cqp"}, Width: 1280, Height: 720},
			"-hwaccel vaapi -hwaccel_device /dev/dri/d -hwaccel_output_format vaapi -protocol_whitelist file -i /in.mkv -map 0:0 -map_metadata -1 -map_chapters 0" +
				" -c:v:0 hevc_vaapi -rc_mode CQP -qp 25 -filter:v:0 scale_vaapi=w=1280:h=720:format=nv12"},
		{"nvenc full", transcode.NVENC, true, transcode.VideoOut{Encoder: "hevc_nvenc", Preset: "slower", Rate: quality(28), Width: 1280, Height: 720, FPS: 24},
			"-hwaccel cuda -hwaccel_output_format cuda -protocol_whitelist file -i /in.mkv -map 0:0 -map_metadata -1 -map_chapters 0" +
				" -c:v:0 hevc_nvenc -preset p6 -rc vbr -cq 28 -b:v 0 -rc-lookahead 32 -spatial_aq 1 -filter:v:0 scale_cuda=1280:720,fps=24"},
		{"nvenc upload", transcode.NVENC, false, transcode.VideoOut{Encoder: "h264_nvenc", Preset: "fast", Rate: transcode.RateControl{Mode: transcode.RateModeBitrate, BitrateKbps: 2000}},
			"-protocol_whitelist file -i /in.mkv -map 0:0 -map_metadata -1 -map_chapters 0" +
				" -c:v:0 h264_nvenc -preset p3 -rc vbr -b:v 2000k -maxrate 3000k -filter:v:0 format=nv12"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			args := build(t, transcode.ConvertSpec{Input: "/in.mkv", Output: "/out.mkv", Container: "mkv", Engine: c.engine, Device: "/dev/dri/d", FullGPU: c.full, Video: []transcode.VideoOut{c.video}})
			assertArgs(t, args, head+" "+c.want+" -max_muxing_queue_size 4096 -f matroska /out.mkv")
		})
	}
}

func TestCanRunOnGPU_RefusesFiltersWithoutAGPUForm(t *testing.T) {
	for _, engine := range []transcode.Accel{transcode.QSV, transcode.VAAPI, transcode.NVENC} {
		for _, kind := range []transcode.VideoFilterKind{transcode.FilterDeband, transcode.FilterDeblock, transcode.FilterCAS, transcode.FilterDeflicker} {
			if transcode.CanRunOnGPU(engine, transcode.VideoOut{Filters: []transcode.VideoFilter{{Kind: kind, Strength: "low"}}}) {
				t.Errorf("%s ran %s on the GPU", engine, kind)
			}
		}
	}
	if transcode.CanRunOnGPU(transcode.Software, transcode.VideoOut{}) {
		t.Error("software is not a GPU")
	}
}

func TestConvertArgs_CopyVideoEncodeAudioKeepsCoversAndFonts(t *testing.T) {
	args := build(t, transcode.ConvertSpec{
		Input: "/in.mkv", Output: "/out.mkv", Kind: transcode.Video, Container: "mkv", Tolerant: true, Engine: transcode.QSV, Device: "d",
		Video: []transcode.VideoOut{{Index: 0, Copy: true}},
		Audio: []transcode.AudioOut{{Index: 1, Encoder: "libopus", BitrateKbps: 256, Channels: 6, SampleRate: 48000,
			Filters: []transcode.AudioFilter{{Kind: transcode.AudioNormalize}}}},
		Subtitles: []transcode.SubtitleOut{{Index: 2, Copy: true, Default: true}}, Covers: []int{3}, KeepFonts: []int{4},
	})
	assertArgs(t, args, head+" -err_detect ignore_err -fflags +igndts+discardcorrupt -max_error_rate 1.0 -protocol_whitelist file -i /in.mkv"+
		" -map 0:0 -map 0:1 -map 0:2 -map 0:3 -map 0:4 -map_metadata -1 -map_chapters 0 -c:v:0 copy -c:v:1 copy -disposition:v:1 attached_pic"+
		" -c:a:0 libopus -vbr on -b:a:0 256k -ac:a:0 6 -ar:a:0 48000 -filter:a:0 loudnorm=I=-16:TP=-1.5:LRA=11,aformat=channel_layouts=5.1 -disposition:a:0 0"+
		" -c:s:0 copy -disposition:s:0 default -c:t copy -max_muxing_queue_size 4096 -f matroska /out.mkv")
}

func TestConvertArgs_FLACToM4AWithEveryAudioFilter(t *testing.T) {
	args := build(t, transcode.ConvertSpec{
		Input: "/in.flac", Output: "/out.m4a", Kind: transcode.Audio, Container: "m4a",
		Audio: []transcode.AudioOut{{Index: 0, Encoder: "aac", BitrateKbps: 256, Channels: 2, SampleRate: 44100, Filters: []transcode.AudioFilter{
			{Kind: transcode.AudioNormalize}, {Kind: transcode.AudioCompress}, {Kind: transcode.AudioDialogEnhance},
			{Kind: transcode.AudioDenoiseNLM, Strength: "strong"}, {Kind: transcode.AudioDeclip}}}},
		Covers: []int{1}, Tags: map[string]string{"MUSICBRAINZ_TRACKID": "abc", "artist": "Vessa Lund"}, Marker: "Hi;1;1;2026-11-24",
	})
	assertArgs(t, args, head+" -protocol_whitelist file -i /in.flac -map 0:0 -map 0:1 -map_metadata -1 -map_chapters 0 -c:v:0 copy -disposition:v:0 attached_pic"+
		" -c:a:0 aac -b:a:0 256k -ac:a:0 2 -ar:a:0 44100 -filter:a:0 adeclip,anlmdn=s=12,dialoguenhance,acompressor=threshold=-18dB:ratio=3:attack=20:release=250,loudnorm=I=-14:TP=-1.5:LRA=8 -disposition:a:0 0"+
		" -metadata MUSICBRAINZ_TRACKID=abc -metadata artist=Vessa Lund -metadata SOLARR_TRANSCODED=Hi;1;1;2026-11-24 -movflags +faststart -max_muxing_queue_size 4096 -f ipod /out.m4a")
}

func TestConvertArgs_ImpossibleSpecs(t *testing.T) {
	ok := transcode.ConvertSpec{Input: "i", Output: "o", Container: "mkv", Video: []transcode.VideoOut{{Copy: true}}}
	for name, mutate := range map[string]func(*transcode.ConvertSpec){
		"no streams":        func(s *transcode.ConvertSpec) { s.Video = nil },
		"unknown container": func(s *transcode.ConvertSpec) { s.Container = "avi" },
		"video no encoder":  func(s *transcode.ConvertSpec) { s.Video = []transcode.VideoOut{{}} },
		"audio no encoder":  func(s *transcode.ConvertSpec) { s.Audio = []transcode.AudioOut{{}} },
		"loudnorm no rate": func(s *transcode.ConvertSpec) {
			s.Audio = []transcode.AudioOut{{Encoder: "aac", Filters: []transcode.AudioFilter{{Kind: transcode.AudioNormalize}}}}
		},
	} {
		spec := ok
		mutate(&spec)
		if _, err := transcode.ConvertArgs(spec); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestConvertArgs_HDRKeepPassesColourTags(t *testing.T) {
	args := build(t, transcode.ConvertSpec{Input: "i", Output: "o", Container: "mkv", Video: []transcode.VideoOut{{Encoder: "libx265", Preset: "normal", Rate: quality(28), TenBit: true,
		HDR: transcode.HDRKeep, ColorPrimaries: "bt2020", ColorTransfer: "smpte2084", ColorSpace: "bt2020nc"}}})
	assertArgs(t, args, head+" -protocol_whitelist file -i i -map 0:0 -map_metadata -1 -map_chapters 0 -c:v:0 libx265 -preset medium -crf 28 -x265-params rc-lookahead=40:log-level=error"+
		" -filter:v:0 format=yuv420p10le -color_primaries:v:0 bt2020 -color_trc:v:0 smpte2084 -colorspace:v:0 bt2020nc -max_muxing_queue_size 4096 -f matroska o")
}
