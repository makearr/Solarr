package transcode_test

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
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
		"-ss", "90.500", "-protocol_whitelist", "file", "-i", "/m/a.mkv",
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
		"-hide_banner", "-loglevel", "error", "-nostdin", "-protocol_whitelist", "file", "-i", "/m/a.flac",
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
		"-hwaccel", "vaapi", "-hwaccel_device", "/dev/dri/renderD128", "-hwaccel_output_format", "vaapi", "-protocol_whitelist", "file", "-i", "/m/a.mkv",
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

func TestArgs_NoHeightCapKeepsTheSourceSize(t *testing.T) {
	args := strings.Join(transcode.Args(transcode.Spec{Kind: transcode.Video, Path: "/a", Accel: transcode.Software, VideoKbps: 5000}), " ")
	if !strings.Contains(args, "-vf scale=-2:'trunc(ih/2)*2',format=yuv420p") {
		t.Fatalf("args %q lack the size-keeping filter", args)
	}
}

type probeRunner struct{ fakeRunner }

func (probeRunner) Run(context.Context, string, ...string) ([]byte, error) {
	return []byte(`{"format":{"bit_rate":"6240000","duration":"61.5"},"streams":[
	 {"codec_type":"video","codec_name":"h264","width":1920,"height":1036,"bit_rate":"5900000"},
	 {"codec_type":"audio","codec_name":"aac","bit_rate":"192000"}]}`), nil
}

func TestProbeSource(t *testing.T) {
	got, err := transcode.NewService(probeRunner{}, nil, nil).ProbeSource(t.Context(), "/m/a.mkv")
	want := transcode.Source{Width: 1920, Height: 1036, VideoCodec: "h264", AudioCodec: "aac", OverallKbps: 6240, VideoKbps: 5900, DurationMs: 61500}
	if err != nil || got != want {
		t.Fatalf("ProbeSource = %+v, %v; want %+v", got, err, want)
	}
}

type scriptedRunner struct {
	hwaccels string
	fails    map[string]string
}

func (scriptedRunner) Lookup(string) error { return nil }

func (r scriptedRunner) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	if slices.Contains(args, "-hwaccels") {
		return []byte(r.hwaccels), nil
	}
	for _, arg := range args {
		if message, ok := r.fails[arg]; ok {
			return nil, errors.New(message)
		}
	}
	return nil, nil
}

func TestDetect_WithoutRenderDeviceSaysHowToPassOne(t *testing.T) {
	found := transcode.Detect(context.Background(), scriptedRunner{}, memWithNode(t, false))
	if len(found) != 1 || found[0].Working || !strings.Contains(found[0].Detail, "--device /dev/dri") {
		t.Fatalf("Detect = %+v; want one failed entry naming --device /dev/dri", found)
	}
}

func TestDetect_FfmpegWithoutVAAPIFailsBeforeTrying(t *testing.T) {
	fsys := memWithNode(t, true)
	found := transcode.Detect(context.Background(), scriptedRunner{hwaccels: "Hardware acceleration methods:\ncuda\n"}, fsys)
	if len(found) == 0 || found[0].Working || found[0].Detail != "this ffmpeg was built without vaapi" {
		t.Fatalf("Detect = %+v; want the missing-vaapi reason", found)
	}
}

func TestDetect_WorkingNodeIsFoundAndFailureGetsPermissionHint(t *testing.T) {
	fsys := memWithNode(t, true)
	found := transcode.Detect(context.Background(), scriptedRunner{hwaccels: "Hardware acceleration methods:\nvaapi\nqsv\n"}, fsys)
	best, ok := transcode.Best(found)
	if !ok || best.Accel != transcode.VAAPI || best.Device != "/dev/dri/renderD128" {
		t.Fatalf("Best = %+v, %v; want the working VAAPI node", best, ok)
	}
	failing := scriptedRunner{fails: map[string]string{"h264_vaapi": "x\nfailed: Permission denied"}}
	found = transcode.Detect(context.Background(), failing, fsys)
	if found[0].Working || !strings.Contains(found[0].Detail, "render group") {
		t.Fatalf("Detect = %+v; want the render-group hint", found[0])
	}
}

func TestFrameDecodeArgs_FollowsThePolicyAndTheBestEncoder(t *testing.T) {
	fsys := memWithNode(t, true)
	service := transcode.NewService(scriptedRunner{}, nil, fsys)
	service.Detect(context.Background())
	want := []string{"-hwaccel", "vaapi", "-hwaccel_device", "/dev/dri/renderD128"}
	if got := service.FrameDecodeArgs("auto", ""); !slices.Equal(got, want) {
		t.Fatalf("auto = %v; want %v", got, want)
	}
	if got := service.FrameDecodeArgs("software", ""); got != nil {
		t.Fatalf("software = %v; want none", got)
	}
}

func memWithNode(t *testing.T, present bool) *fsx.Mem {
	t.Helper()
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	if !present {
		return fsys
	}
	if err := fsys.MkdirAll("/dev/dri", 0o755); err != nil {
		t.Fatal(err)
	}
	writer, err := fsys.Create("/dev/dri/renderD128")
	if err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	return fsys
}

type subtitleRunner struct{ fakeRunner }

func (subtitleRunner) Run(context.Context, string, ...string) ([]byte, error) {
	return []byte(`{"streams":[
	 {"codec_name":"subrip","tags":{"language":"eng","title":"English"},"disposition":{"default":1}},
	 {"codec_name":"hdmv_pgs_subtitle","tags":{"language":"deu"},"disposition":{"default":0}},
	 {"codec_name":"ass","tags":{"language":"fra"},"disposition":{"default":0}}]}`), nil
}

func TestSubtitleTracks_ListTextStreamsAndSidecarsAndSkipPictures(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	if err := fsys.MkdirAll("/m", 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"/m/film.mkv", "/m/film.nl.srt", "/m/other.srt"} {
		writer, err := fsys.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_ = writer.Close()
	}
	got, err := transcode.NewService(subtitleRunner{}, nil, fsys).SubtitleTracks(t.Context(), "/m/film.mkv")
	want := []transcode.SubtitleTrack{
		{ID: 0, Language: "eng", Title: "English", Codec: "subrip", Default: true},
		{ID: 2, Language: "fra", Codec: "ass"},
		{ID: transcode.SidecarBase, Language: "nl", Title: "film.nl.srt", Codec: "srt"},
	}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("SubtitleTracks = %+v, %v; want %+v", got, err, want)
	}
}

func TestStream_ANewerConversionByTheSameOwnerTakesTheSlot(t *testing.T) {
	service := transcode.NewService(fakeRunner{}, waitingPiper{}, nil)
	spec := transcode.Spec{Owner: "1//a", Kind: transcode.Audio, Path: "/a"}
	first := make(chan error, 1)
	go func() { first <- service.Stream(t.Context(), io.Discard, spec, 1) }()
	for service.Active() == 0 {
	}
	second := make(chan error, 1)
	go func() { second <- service.Stream(t.Context(), io.Discard, spec, 1) }()
	if err := <-first; err != nil {
		t.Fatalf("replaced Stream = %v, want nil", err)
	}
	for service.Active() != 1 {
	}
	other := transcode.Spec{Owner: "2//a", Kind: transcode.Audio, Path: "/a"}
	if err := service.Stream(t.Context(), io.Discard, other, 1); !errors.Is(err, transcode.ErrBusy) {
		t.Fatalf("another owner's Stream = %v, want ErrBusy", err)
	}
}

type waitingPiper struct{}

func (waitingPiper) Pipe(ctx context.Context, _ io.Writer, _ string, _ ...string) error {
	<-ctx.Done()
	return nil
}

// scriptedPiper plays what each call does: write sends bytes first, hang then blocks until its context ends.
type scriptedPiper struct {
	write []string
	hang  []bool
	calls int
}

func (p *scriptedPiper) Pipe(ctx context.Context, w io.Writer, _ string, _ ...string) error {
	call := p.calls
	p.calls++
	if call < len(p.write) && p.write[call] != "" {
		if _, err := w.Write([]byte(p.write[call])); err != nil {
			return err
		}
	}
	if call < len(p.hang) && p.hang[call] {
		<-ctx.Done()
	}
	return nil
}

// advanceUntil moves the fake clock in steps until done reports true, so the watchdog's ticker fires.
func advanceUntil(t *testing.T, fake *clock.Fake, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatal("the condition never came true")
		}
		fake.Advance(5 * time.Second)
		time.Sleep(2 * time.Millisecond)
	}
}

func TestStream_AHungConversionIsEndedAndTheNextStepTriesAtOnce(t *testing.T) {
	fake := clock.NewFakeAtEpoch()
	piper := &scriptedPiper{hang: []bool{true, false}, write: []string{"", "data"}}
	service := transcode.NewService(fakeRunner{}, piper, nil).WithStallWatch(fake, 40*time.Second)
	spec := transcode.Spec{Kind: transcode.Video, Path: "/m/a.mkv", Accel: transcode.NVENC, Device: "d", MaxHeight: 720, VideoKbps: 4000}

	result := make(chan error, 1)
	go func() { result <- service.Stream(t.Context(), io.Discard, spec, 2) }()
	advanceUntil(t, fake, func() bool { return len(result) == 1 })
	if err := <-result; err != nil {
		t.Fatalf("Stream = %v, want the next step to finish the stream", err)
	}
	if piper.calls != 2 {
		t.Fatalf("calls = %d, want the hung step and the one after it", piper.calls)
	}
}

func TestStream_AHungConversionAfterBytesWentOutEndsWithErrStalled(t *testing.T) {
	fake := clock.NewFakeAtEpoch()
	piper := &scriptedPiper{hang: []bool{true}, write: []string{"data"}}
	service := transcode.NewService(fakeRunner{}, piper, nil).WithStallWatch(fake, 40*time.Second)
	spec := transcode.Spec{Kind: transcode.Video, Path: "/m/a.mkv", Accel: transcode.Software, MaxHeight: 720, VideoKbps: 4000}

	result := make(chan error, 1)
	go func() { result <- service.Stream(t.Context(), io.Discard, spec, 2) }()
	advanceUntil(t, fake, func() bool { return len(result) == 1 })
	if err := <-result; !errors.Is(err, transcode.ErrStalled) {
		t.Fatalf("Stream = %v, want ErrStalled", err)
	}
}

// pausedWriter is a viewer who stopped reading: Write does not return until released.
type pausedWriter struct{ release chan struct{} }

func (p pausedWriter) Write(b []byte) (int, error) { <-p.release; return len(b), nil }

func TestStream_AViewerWhoPausedIsNotAStall(t *testing.T) {
	fake := clock.NewFakeAtEpoch()
	release := make(chan struct{})
	piper := &scriptedPiper{write: []string{"data"}}
	service := transcode.NewService(fakeRunner{}, piper, nil).WithStallWatch(fake, 40*time.Second)
	spec := transcode.Spec{Kind: transcode.Video, Path: "/m/a.mkv", Accel: transcode.Software, MaxHeight: 720, VideoKbps: 4000}

	result := make(chan error, 1)
	go func() { result <- service.Stream(t.Context(), pausedWriter{release}, spec, 2) }()
	for range 40 {
		fake.Advance(5 * time.Second)
		time.Sleep(2 * time.Millisecond)
	}
	if len(result) != 0 {
		t.Fatal("a paused viewer ended the stream")
	}
	close(release)
	if err := <-result; err != nil {
		t.Fatalf("Stream = %v after the viewer resumed, want nil", err)
	}
}
