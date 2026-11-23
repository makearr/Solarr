package transcode_test

import (
	"context"
	"encoding/base64"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/makearr/Solarr/internal/platform/mediatool"
	"github.com/makearr/Solarr/internal/platform/transcode"
)

// stdoutRunner keeps ffprobe's warnings out of the JSON the way the production runner does.
type stdoutRunner struct{}

func (stdoutRunner) Lookup(name string) error { _, err := exec.LookPath(name); return err }

func (stdoutRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

func requireMediaTools(t *testing.T) {
	t.Helper()
	for _, binary := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Skipf("%s is not on PATH", binary)
		}
	}
}

func dataURI(text string) string {
	return "data:text/plain;base64," + base64.StdEncoding.EncodeToString([]byte(text))
}

func ffmpeg(t *testing.T, args ...string) {
	t.Helper()
	if output, err := exec.CommandContext(t.Context(), "ffmpeg", append([]string{"-v", "error", "-y"}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg %v: %v: %s", args, err, output)
	}
}

func convert(t *testing.T, spec transcode.ConvertSpec) {
	t.Helper()
	args, err := transcode.ConvertArgs(spec)
	if err != nil {
		t.Fatal(err)
	}
	if output, err := exec.CommandContext(t.Context(), "ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg %v: %v: %s", args, err, output)
	}
}

func TestConvertArgs_RealFFmpegMKVToMP4KeepsStreamsChaptersAndMarker(t *testing.T) {
	requireMediaTools(t)
	directory := t.TempDir()
	source, output := filepath.Join(directory, "in.mkv"), filepath.Join(directory, "out.mp4")
	chapters := ";FFMETADATA1\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=0\nEND=1500\ntitle=One\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=1500\nEND=3000\ntitle=Two\n"
	ffmpeg(t, "-f", "lavfi", "-i", "testsrc=duration=3:size=320x240:rate=25", "-f", "lavfi", "-i", "sine=duration=3", "-f", "lavfi", "-i", "sine=frequency=880:duration=3",
		"-f", "srt", "-i", dataURI("1\n00:00:00,000 --> 00:00:02,000\nHello\n"), "-f", "ffmetadata", "-i", dataURI(chapters),
		"-map", "0:v", "-map", "1:a", "-map", "2:a", "-map", "3:s", "-map_chapters", "4", "-c:v", "libx264", "-c:a", "aac", "-c:s", "srt",
		"-metadata:s:a:0", "language=eng", "-metadata:s:a:1", "language=deu", "-metadata", "encoder_junk=drop", source)
	service := transcode.NewService(stdoutRunner{}, nil, nil)
	in, err := service.ProbeStreams(t.Context(), source)
	if err != nil || in.Chapters != 2 || len(in.Streams) != 4 {
		t.Fatalf("source = %+v, %v", in, err)
	}
	convert(t, transcode.ConvertSpec{
		Input: source, Output: output, Kind: transcode.Video, Container: "mp4", Engine: transcode.Software,
		Video:     []transcode.VideoOut{{Index: 0, Encoder: "libx265", Rate: quality(30), Preset: "fast"}},
		Audio:     []transcode.AudioOut{{Index: 1, Encoder: "aac", BitrateKbps: 96, Channels: 1, SampleRate: 44100, Language: "en", Default: true}, {Index: 2, Copy: true, Language: "de"}},
		Subtitles: []transcode.SubtitleOut{{Index: 3, Codec: "mov_text", Language: "en"}},
		Tags:      map[string]string{"title": "Pilot"}, CustomTags: true, Marker: transcode.MarkerValue("Example", 1, 1, "2026-11-24"),
	})
	out, err := service.ProbeStreams(t.Context(), output)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Streams) != 4 || out.Streams[0].Codec != "hevc" || out.Streams[1].Codec != "aac" || out.Streams[1].Language != "en" || out.Streams[2].Language != "de" || out.Streams[3].Codec != "mov_text" {
		t.Errorf("streams = %+v", out.Streams)
	}
	if out.Chapters != 2 {
		t.Errorf("chapters = %d, want 2", out.Chapters)
	}
	if !transcode.HasMarker(out.Tags) || out.Tags["title"] != "Pilot" {
		t.Errorf("tags = %v, want the marker and the title", out.Tags)
	}
	if _, junk := out.Tags["encoder_junk"]; junk {
		t.Errorf("a dropped tag survived: %v", out.Tags)
	}
}

// Finding: MP4 keeps a cover or custom tags, never both, so the caller copies the custom tags afterwards when there is a cover.
func TestConvertArgs_RealFFmpegFLACToM4AKeepsCoverOrCustomTagsNeverBoth(t *testing.T) {
	requireMediaTools(t)
	directory := t.TempDir()
	cover, flac := filepath.Join(directory, "c.jpg"), filepath.Join(directory, "in.flac")
	ffmpeg(t, "-f", "lavfi", "-i", "testsrc=size=64x64", "-frames:v", "1", cover)
	ffmpeg(t, "-f", "lavfi", "-i", "sine=duration=2", "-i", cover, "-map", "0", "-map", "1", "-c:a", "flac", "-c:v", "copy", "-disposition:v", "attached_pic",
		"-metadata", "MUSICBRAINZ_TRACKID=track-mbid", "-metadata", "artist=Vessa Lund", flac)
	run := func(custom bool) (transcode.File, mediatool.Probe) {
		output := filepath.Join(directory, "out.m4a")
		convert(t, transcode.ConvertSpec{
			Input: flac, Output: output, Kind: transcode.Audio, Container: "m4a", CustomTags: custom,
			Audio:  []transcode.AudioOut{{Index: 0, Encoder: "aac", BitrateKbps: 128, Channels: 1, SampleRate: 44100, Filters: []transcode.AudioFilter{{Kind: transcode.AudioNormalize}}}},
			Covers: []int{1}, Tags: map[string]string{"artist": "Vessa Lund", "MUSICBRAINZ_TRACKID": "track-mbid"}, Marker: transcode.MarkerValue("Lossy", 2, 1, "2026-11-24"),
		})
		file, err := transcode.NewService(stdoutRunner{}, nil, nil).ProbeStreams(t.Context(), output)
		if err != nil {
			t.Fatal(err)
		}
		probes, err := mediatool.New(nil, 1).ReadTags(t.Context(), []string{output})
		if err != nil || probes[0].Err != nil {
			t.Fatalf("ReadTags: %v %v", err, probes[0].Err)
		}
		return file, probes[0]
	}
	file, probe := run(false)
	if len(file.Streams) != 2 || file.Streams[0].Codec != "aac" || file.Streams[0].SampleRate != 44100 || !file.Streams[1].AttachedPicture || probe.Tags.First("artist") != "Vessa Lund" {
		t.Errorf("without custom tags: streams = %+v, artist = %q; want aac at 44100, a cover and the artist", file.Streams, probe.Tags.First("artist"))
	}
	file, probe = run(true)
	if len(file.Streams) != 1 || probe.Tags.First("musicbrainz_trackid") != "track-mbid" || !transcode.HasMarker(file.Tags) {
		t.Errorf("with custom tags: streams = %+v, mbid = %q, tags = %v; want the MBID and the marker and no cover", file.Streams, probe.Tags.First("musicbrainz_trackid"), file.Tags)
	}
}

func TestConvertArgs_RealFFmpegEverySoftwareFilterAtEveryStrength(t *testing.T) {
	requireMediaTools(t)
	directory := t.TempDir()
	source := filepath.Join(directory, "in.mkv")
	ffmpeg(t, "-f", "lavfi", "-i", "testsrc=duration=1:size=320x240:rate=25", "-f", "lavfi", "-i", "sine=duration=1", "-c:v", "libx264", "-c:a", "aac", source)
	kinds := []transcode.VideoFilterKind{transcode.FilterDeflicker, transcode.FilterHQDN3D, transcode.FilterNLMeans, transcode.FilterBM3D, transcode.FilterDeblock, transcode.FilterDeband, transcode.FilterCAS, transcode.FilterSmartBlur}
	for _, kind := range kinds {
		for _, strength := range []string{"low", "medium", "strong"} {
			t.Run(string(kind)+"/"+strength, func(t *testing.T) {
				convert(t, transcode.ConvertSpec{Input: source, Output: filepath.Join(t.TempDir(), "o.mkv"), Container: "mkv",
					Video: []transcode.VideoOut{{Index: 0, Encoder: "libx264", Preset: "fast", Rate: quality(30), Filters: []transcode.VideoFilter{{Kind: kind, Strength: strength}}}}})
			})
		}
	}
	t.Run("deinterlace scale fps tonemap", func(t *testing.T) {
		hdr := filepath.Join(t.TempDir(), "hdr.mkv")
		ffmpeg(t, "-f", "lavfi", "-i", "testsrc=duration=1:size=320x240:rate=25", "-vf", "setparams=color_primaries=bt2020:color_trc=smpte2084:colorspace=bt2020nc", "-c:v", "libx265", "-pix_fmt", "yuv420p10le", hdr)
		convert(t, transcode.ConvertSpec{Input: hdr, Output: filepath.Join(t.TempDir(), "o.mkv"), Container: "mkv",
			Video: []transcode.VideoOut{{Index: 0, Encoder: "libx264", Preset: "fast", Rate: quality(30), Width: 160, Height: 120, FPS: 12, HDR: transcode.HDRTonemap,
				Filters: []transcode.VideoFilter{{Kind: transcode.FilterDeinterlace}}}}})
	})
	for _, filter := range []transcode.AudioFilterKind{transcode.AudioDeclip, transcode.AudioDenoiseFFT, transcode.AudioDenoiseNLM, transcode.AudioDialogEnhance, transcode.AudioCompress, transcode.AudioNormalize} {
		for _, strength := range []string{"low", "medium", "strong"} {
			t.Run("audio/"+string(filter)+"/"+strength, func(t *testing.T) {
				convert(t, transcode.ConvertSpec{Input: source, Output: filepath.Join(t.TempDir(), "o.mkv"), Container: "mkv", Video: []transcode.VideoOut{{Index: 0, Copy: true}},
					Audio: []transcode.AudioOut{{Index: 1, Encoder: "libopus", BitrateKbps: 96, Channels: 2, SampleRate: 48000, Filters: []transcode.AudioFilter{{Kind: filter, Strength: strength}}}}})
			})
		}
	}
}
