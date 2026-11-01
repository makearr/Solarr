package transcode_test

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/platform/transcode"
)

type fakeRunner struct {
	missing bool
	// fails names the encoders that error.
	fails map[string]bool
}

func (f fakeRunner) Lookup(string) error {
	if f.missing {
		return errors.New("missing")
	}
	return nil
}

func (f fakeRunner) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	for _, arg := range args {
		if f.fails[arg] {
			return nil, errors.New("line one\nDevice creation failed")
		}
	}
	return nil, nil
}

func TestBest_PrefersNVENCThenVAAPIThenQSVAndSkipsFailures(t *testing.T) {
	found := []transcode.Capability{
		{Accel: transcode.QSV, Working: true, Device: "q"},
		{Accel: transcode.VAAPI, Working: false},
		{Accel: transcode.VAAPI, Working: true, Device: "v"},
	}
	best, ok := transcode.Best(found)
	if !ok || best.Accel != transcode.VAAPI || best.Device != "v" {
		t.Fatalf("Best = %+v, %v; want the working VAAPI", best, ok)
	}
	if _, ok := transcode.Best(found[1:2]); ok {
		t.Fatal("a failing encoder was chosen")
	}
}

func TestArgs_VAAPIVideoUploadsFramesAndCapsHeight(t *testing.T) {
	args := transcode.Args(transcode.Spec{
		Kind: transcode.Video, Path: "/m/a.mkv", StartSecond: 90.5, Accel: transcode.VAAPI,
		Device: "/dev/dri/renderD128", MaxHeight: 720, VideoKbps: 4000,
	})
	want := []string{
		"-hide_banner", "-loglevel", "error", "-nostdin",
		"-init_hw_device", "vaapi=hw:/dev/dri/renderD128", "-filter_hw_device", "hw",
		"-ss", "90.500", "-i", "/m/a.mkv",
		"-map", "0:v:0", "-map", "0:a:0?", "-vf", "scale=-2:'trunc(min(ih,720)/2)*2',format=nv12,hwupload",
		"-c:v", "h264_vaapi", "-b:v", "4000k", "-maxrate", "4000k", "-bufsize", "8000k", "-g", "48",
		"-c:a", "aac", "-b:a", "160k", "-ac", "2",
		"-movflags", "frag_keyframe+empty_moov+default_base_moof", "-f", "mp4", "pipe:1",
	}
	if !slices.Equal(args, want) {
		t.Fatalf("Args =\n%q\nwant\n%q", args, want)
	}
}

func TestArgs_AudioDropsVideoAndWritesMP3(t *testing.T) {
	args := transcode.Args(transcode.Spec{Kind: transcode.Audio, Path: "/m/a.flac"})
	want := []string{
		"-hide_banner", "-loglevel", "error", "-nostdin", "-i", "/m/a.flac",
		"-vn", "-map", "0:a:0", "-c:a", "libmp3lame", "-b:a", "192k", "-f", "mp3", "pipe:1",
	}
	if !slices.Equal(args, want) {
		t.Fatalf("Args = %q, want %q", args, want)
	}
}

func TestResolve(t *testing.T) {
	service := transcode.NewService(fakeRunner{}, nil, nil)
	if got := service.Resolve("auto", ""); got.Accel != transcode.Software || got.Note != "" {
		t.Fatalf("auto with nothing detected = %+v, want quiet software", got)
	}
	if got := service.Resolve("vaapi", ""); got.Accel != transcode.Software || got.Note == "" {
		t.Fatalf("forced vaapi that never worked = %+v, want software with a note", got)
	}
	if got := service.Resolve("vaapi", "/dev/dri/renderD129"); got.Accel != transcode.VAAPI || got.Device != "/dev/dri/renderD129" {
		t.Fatalf("forced vaapi with a device = %+v, want the operator's device trusted", got)
	}
}

type fakePiper struct {
	// failFirst is how many calls fail before one succeeds.
	failFirst int
	calls     [][]string
}

func (f *fakePiper) Pipe(_ context.Context, w io.Writer, _ string, args ...string) error {
	f.calls = append(f.calls, args)
	if len(f.calls) <= f.failFirst {
		return errors.New("encoder failed")
	}
	_, err := w.Write([]byte("data"))
	return err
}

func TestArgs_VAAPIHardwareDecodeKeepsFramesOnTheGPU(t *testing.T) {
	args := transcode.Args(transcode.Spec{
		Kind: transcode.Video, Path: "/m/a.mkv", Accel: transcode.VAAPI, Device: "/dev/dri/renderD128",
		MaxHeight: 720, VideoKbps: 4000, HardwareDecode: true,
	})
	want := []string{
		"-hwaccel", "vaapi", "-hwaccel_device", "/dev/dri/renderD128", "-hwaccel_output_format", "vaapi", "-i", "/m/a.mkv",
		"-map", "0:v:0", "-map", "0:a:0?", "-vf", "scale_vaapi=w=-2:h='trunc(min(ih,720)/2)*2':format=nv12", "-c:v", "h264_vaapi",
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, strings.Join(want, " ")) {
		t.Fatalf("Args = %q, want it to contain %q", args, want)
	}
}

func TestStream_StepsDownFromFullGPUToEncodeOnlyToSoftware(t *testing.T) {
	piper := &fakePiper{failFirst: 2}
	service := transcode.NewService(fakeRunner{}, piper, nil)
	spec := transcode.Spec{Kind: transcode.Video, Path: "/m/a.mkv", Accel: transcode.VAAPI, Device: "d", MaxHeight: 1080, VideoKbps: 6000}
	if err := service.Stream(t.Context(), io.Discard, spec, 2); err != nil {
		t.Fatalf("Stream = %v", err)
	}
	if len(piper.calls) != 3 || !slices.Contains(piper.calls[0], "-hwaccel") || slices.Contains(piper.calls[1], "-hwaccel") ||
		!slices.Contains(piper.calls[1], "h264_vaapi") || !slices.Contains(piper.calls[2], "libx264") {
		t.Fatalf("calls = %q, want GPU decode, then GPU encode, then software", piper.calls)
	}
}

func TestStream_FallsBackToSoftwareWhenTheGPUEncoderFailsBeforeAnyByte(t *testing.T) {
	piper := &fakePiper{failFirst: 2}
	service := transcode.NewService(fakeRunner{}, piper, nil)
	var out strings.Builder
	spec := transcode.Spec{Kind: transcode.Video, Path: "/m/a.mkv", Accel: transcode.VAAPI, Device: "d", MaxHeight: 1080, VideoKbps: 6000}
	if err := service.Stream(t.Context(), &out, spec, 2); err != nil {
		t.Fatalf("Stream = %v", err)
	}
	if out.String() != "data" || len(piper.calls) != 3 || !slices.Contains(piper.calls[2], "libx264") {
		t.Fatalf("out %q, calls %d; want the software retry's output", out.String(), len(piper.calls))
	}
}

func TestStream_RefusesPastTheSessionLimit(t *testing.T) {
	block := make(chan struct{})
	service := transcode.NewService(fakeRunner{}, blockingPiper{block}, nil)
	spec := transcode.Spec{Kind: transcode.Audio, Path: "/a"}
	done := make(chan error, 1)
	go func() { done <- service.Stream(t.Context(), io.Discard, spec, 1) }()
	for service.Active() == 0 {
	}
	if err := service.Stream(t.Context(), io.Discard, spec, 1); !errors.Is(err, transcode.ErrBusy) {
		t.Fatalf("second Stream = %v, want ErrBusy", err)
	}
	close(block)
	if err := <-done; err != nil {
		t.Fatalf("first Stream = %v", err)
	}
}

type blockingPiper struct{ release chan struct{} }

func (b blockingPiper) Pipe(context.Context, io.Writer, string, ...string) error {
	<-b.release
	return nil
}

func TestStream_WithoutFFmpegIsUnavailable(t *testing.T) {
	service := transcode.NewService(fakeRunner{missing: true}, nil, nil)
	if err := service.Stream(t.Context(), io.Discard, transcode.Spec{}, 1); !errors.Is(err, transcode.ErrUnavailable) {
		t.Fatalf("Stream = %v, want ErrUnavailable", err)
	}
}
