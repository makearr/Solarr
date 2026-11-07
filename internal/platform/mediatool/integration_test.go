package mediatool_test

import (
	"bytes"
	"image"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// requireFFmpeg skips rather than fails: CI builds in a Go image with no media
// tools, and a test that cannot run is not a test that failed.
func requireFFmpeg(t *testing.T) {
	t.Helper()
	for _, binary := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Skipf("%s is not on PATH", binary)
		}
	}
}

// generate writes a real encoded file, because the point of this test is that the
// parser survives what ffprobe actually emits rather than what a fixture claims.
func generate(t *testing.T, directory, name, codec string) string {
	t.Helper()
	path := filepath.Join(directory, name)
	command := exec.CommandContext(t.Context(), "ffmpeg", "-v", "error", "-y",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2",
		"-c:a", codec,
		"-metadata", "MUSICBRAINZ_TRACKID=track-mbid",
		"-metadata", "artist=Nick Cave",
		path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generate %s: %v: %s", name, err, output)
	}
	return path
}

// The one that matters for ADR-29: identity comes from an embedded MBID, so the
// read path has to recover it from every format the library holds.
func TestReadTags_AgainstRealFFprobe(t *testing.T) {
	requireFFmpeg(t)
	directory := t.TempDir()
	paths := []string{
		generate(t, directory, "a.flac", "flac"),
		generate(t, directory, "b.mp3", "libmp3lame"),
	}

	// M4A is measured but not tag-asserted: ffmpeg cannot write the custom tag to
	// an MP4 freeform atom, which is the finding that ruled it out as the tagger
	// (documentation/media-tools.md). Reading one is still a distinct parse path.
	m4a := generate(t, directory, "c.m4a", "aac")
	m4aProbe, err := mediatool.New(nil, 1).ReadTags(t.Context(), []string{m4a})
	if err != nil {
		t.Fatalf("ReadTags m4a: %v", err)
	}
	if m4aProbe[0].Err != nil || m4aProbe[0].Measured.AudioCodec == "" {
		t.Errorf("m4a probe = %+v, want a codec and no error", m4aProbe[0])
	}
	if got := m4aProbe[0].Tags.First("artist"); got != "Nick Cave" {
		t.Errorf("m4a artist = %q, want Nick Cave: standard tags do survive", got)
	}

	probes, err := mediatool.New(nil, 4).ReadTags(t.Context(), paths)
	if err != nil {
		t.Fatalf("ReadTags: %v", err)
	}
	for index, probe := range probes {
		if probe.Err != nil {
			t.Fatalf("probe %s: %v", paths[index], probe.Err)
		}
		if got := probe.Tags.First("musicbrainz_trackid"); got != "track-mbid" {
			t.Errorf("%s: MBID = %q, want track-mbid", filepath.Base(paths[index]), got)
		}
		if got := probe.Tags.First("artist"); got != "Nick Cave" {
			t.Errorf("%s: artist = %q, want Nick Cave", filepath.Base(paths[index]), got)
		}
		if probe.Measured.Duration < time.Second || probe.Measured.Duration > 3*time.Second {
			t.Errorf("%s: duration = %s, want about 2s", filepath.Base(paths[index]), probe.Measured.Duration)
		}
		if probe.Measured.SampleRate == 0 || probe.Measured.AudioCodec == "" {
			t.Errorf("%s: measured %+v, want a codec and a sample rate", filepath.Base(paths[index]), probe.Measured)
		}
	}
}

func TestReadTags_AgainstRealFFprobeReportsAFileThatIsNotMedia(t *testing.T) {
	requireFFmpeg(t)
	directory := t.TempDir()
	good := generate(t, directory, "a.flac", "flac")
	junk := filepath.Join(directory, "notes.txt")
	if err := os.WriteFile(junk, []byte("this is not audio"), 0o600); err != nil {
		t.Fatalf("write junk: %v", err)
	}

	probes, err := mediatool.New(nil, 2).ReadTags(t.Context(), []string{good, junk})
	if err != nil {
		t.Fatalf("ReadTags: %v", err)
	}
	if probes[0].Err != nil {
		t.Errorf("the real file failed: %v", probes[0].Err)
	}
	if probes[1].Err == nil {
		t.Error("a text file probed successfully")
	}
}

// The language keys are spelled by ffprobe, not by us: ID3 puts the code in the key, Vorbis in LANGUAGE.
func TestReadTags_LyricsLanguageAgainstRealFFprobe(t *testing.T) {
	requireFFmpeg(t)
	directory := t.TempDir()
	write := func(name, codec string, metadata ...string) string {
		path := filepath.Join(directory, name)
		args := []string{"-v", "error", "-y", "-f", "lavfi", "-i", "sine=frequency=440:duration=1", "-c:a", codec}
		for _, entry := range metadata {
			args = append(args, "-metadata", entry)
		}
		if output, err := exec.CommandContext(t.Context(), "ffmpeg", append(args, path)...).CombinedOutput(); err != nil {
			t.Fatalf("generate %s: %v: %s", name, err, output)
		}
		return path
	}
	mp3 := write("a.mp3", "libmp3lame", "lyrics-deu=Hallo Welt")
	flac := write("b.flac", "flac", "LYRICS=Hello", "LANGUAGE=eng")
	plain := write("c.flac", "flac")

	probes, err := mediatool.New(nil, 1).ReadTags(t.Context(), []string{mp3, flac, plain})
	if err != nil {
		t.Fatalf("ReadTags: %v", err)
	}
	var got [][]string
	for _, probe := range probes {
		got = append(got, probe.Measured.LyricsLanguages)
	}
	if want := [][]string{{"de"}, {"en"}, nil}; !reflect.DeepEqual(got, want) {
		t.Errorf("lyrics languages = %v, want %v", got, want)
	}
}

// The same synthetic video re-made at another size, frame rate, codec, aspect ratio (letterboxed) or start (a few frames
// later) hashes within a few bits, mean and moment by moment; another picture does not, nor does the same footage played backwards.
func TestPerceptualHash_AgainstRealFFmpeg(t *testing.T) {
	requireFFmpeg(t)
	directory := t.TempDir()
	encode := func(name, source, filter string, extra ...string) string {
		path := filepath.Join(directory, name)
		args := append([]string{"-v", "error", "-y", "-f", "lavfi", "-t", "10", "-i", source, "-vf", filter}, extra...)
		command := exec.CommandContext(t.Context(), "ffmpeg", append(args, path)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("encode %s: %v: %s", name, err, output)
		}
		return path
	}
	tool := mediatool.New(mediatool.ExecRunner{}, 1)
	hash := func(path string) mediatool.VideoHash {
		value, err := tool.PerceptualHash(t.Context(), path)
		if err != nil {
			t.Fatalf("PerceptualHash(%s): %v", path, err)
		}
		return value
	}
	matching := func(a, b mediatool.VideoHash) int {
		matched := 0
		for slot := range 9 {
			near := false
			for _, first := range a.Frames {
				for _, second := range b.Frames {
					if first.Slot == slot && second.Slot == slot && mediatool.PerceptualDistance(first.Hash, second.Hash) <= 10 {
						near = true
					}
				}
			}
			if near {
				matched++
			}
		}
		return matched
	}
	cut := "mandelbrot=size=640x360:rate=10"
	reference := hash(encode("large.mkv", cut, "null", "-s", "640x360", "-c:v", "mpeg4", "-b:v", "2M"))
	for _, variant := range []struct {
		name string
		path string
	}{
		{"smaller and cheaper", encode("small.mkv", cut, "null", "-s", "320x180", "-c:v", "mpeg4", "-b:v", "300k")},
		{"other codec and frame rate", encode("rate.mkv", cut, "null", "-r", "25", "-s", "640x360", "-c:v", "libx264", "-crf", "30")},
		{"letterboxed to 2.39:1", encode("box.mkv", cut, "scale=640:268,pad=640:360:0:46:black", "-c:v", "mpeg4", "-b:v", "2M")},
		{"pillarboxed into 4:3", encode("pillar.mkv", cut, "scale=270:360,pad=480:360:105:0:black", "-c:v", "mpeg4", "-b:v", "2M")},
		{"starting three frames later", encode("late.mkv", cut, "trim=start=0.3,setpts=PTS-STARTPTS", "-c:v", "mpeg4", "-b:v", "2M")},
	} {
		got := hash(variant.path)
		if matched := matching(reference, got); matched < 7 {
			t.Errorf("%s: %d of 9 moments match, want >= 7", variant.name, matched)
		}
		if distance := mediatool.PerceptualDistance(reference.Mean, got.Mean); distance > 10 {
			t.Errorf("%s: mean hashes %d bits apart, want <= 10", variant.name, distance)
		}
	}
	other := hash(encode("other.mkv", "testsrc2=size=640x360:rate=10:duration=10", "null", "-c:v", "mpeg4", "-b:v", "2M"))
	if got := mediatool.PerceptualDistance(reference.Mean, other.Mean); got < 12 {
		t.Errorf("different pictures: %d bits apart, want >= 12", got)
	}
	if got := matching(reference, other); got >= 6 {
		t.Errorf("different pictures: %d moments match, want fewer than two thirds", got)
	}
	backwards := hash(encode("backwards.mkv", cut, "reverse", "-c:v", "mpeg4", "-b:v", "2M"))
	t.Logf("backwards: mean %d bits apart, %d of 9 moments match", mediatool.PerceptualDistance(reference.Mean, backwards.Mean), matching(reference, backwards))
	if got := matching(reference, backwards); got >= 6 {
		t.Errorf("the cut played backwards: %d moments match, want fewer than two thirds", got)
	}
}

// One read of the video gives the same hash as the hash alone, plus a single frame at the video's own shape and a 3x3
// sheet of 426x240 cells.
func TestHashAndPreview_AgainstRealFFmpeg(t *testing.T) {
	requireFFmpeg(t)
	path := filepath.Join(t.TempDir(), "wide.mkv")
	command := exec.CommandContext(t.Context(), "ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-t", "10", "-i", "testsrc2=size=1920x800:rate=25",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("encode: %v: %s", err, output)
	}
	tool := mediatool.New(mediatool.ExecRunner{}, 1)
	alone, err := tool.PerceptualHash(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	hash, preview, err := tool.HashAndPreview(t.Context(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	if hash.Mean != alone.Mean || len(hash.Frames) != len(alone.Frames) {
		t.Errorf("hash with previews %x/%d frames, without %x/%d", hash.Mean, len(hash.Frames), alone.Mean, len(alone.Frames))
	}
	for name, want := range map[string]image.Rectangle{"frame": image.Rect(0, 0, 1280, 534), "sheet": image.Rect(0, 0, 3*426, 3*178)} {
		data := map[string][]byte{"frame": preview.Frame, "sheet": preview.Sheet}[name]
		picture, err := jpeg.Decode(bytes.NewReader(data))
		if err != nil || picture.Bounds() != want {
			t.Errorf("%s: %v %v, want %v", name, picture != nil && picture.Bounds() == want, err, want)
		}
	}
}
