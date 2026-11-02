package mediatool_test

import (
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

// The same synthetic video encoded twice (size and bitrate differ) hashes within a few bits, mean and frame by frame;
// another picture does not, and neither does the same footage played backwards, though its mean frame is the same.
func TestPerceptualHash_AgainstRealFFmpeg(t *testing.T) {
	requireFFmpeg(t)
	directory := t.TempDir()
	encode := func(name, source, filter, size, bitrate string) string {
		path := filepath.Join(directory, name)
		command := exec.CommandContext(t.Context(), "ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-t", "10", "-i", source,
			"-vf", filter, "-s", size, "-c:v", "mpeg4", "-b:v", bitrate, path)
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
		for _, first := range a.Frames {
			for _, second := range b.Frames {
				if first.Slot == second.Slot && mediatool.PerceptualDistance(first.Hash, second.Hash) <= 10 {
					matched++
				}
			}
		}
		return matched
	}
	cut := "mandelbrot=size=640x360:rate=10"
	large := hash(encode("large.mkv", cut, "null", "640x360", "2M"))
	small := hash(encode("small.mkv", cut, "null", "320x180", "300k"))
	backwards := hash(encode("backwards.mkv", cut, "reverse", "640x360", "2M"))
	other := hash(encode("other.mkv", "testsrc2=size=640x360:rate=10:duration=10", "null", "640x360", "2M"))
	if got := mediatool.PerceptualDistance(large.Mean, small.Mean); got > 8 {
		t.Errorf("same cut, two encodes: %d bits apart, want <= 8", got)
	}
	if got := mediatool.PerceptualDistance(large.Mean, other.Mean); got < 12 {
		t.Errorf("different pictures: %d bits apart, want >= 12", got)
	}
	if len(large.Frames) != 7 || matching(large, small) != 7 {
		t.Errorf("same cut, two encodes: %d of %d frames match, want 7 of 7", matching(large, small), len(large.Frames))
	}
	t.Logf("backwards: mean %d bits apart, %d of 7 frames match", mediatool.PerceptualDistance(large.Mean, backwards.Mean), matching(large, backwards))
	if got := matching(large, backwards); got >= 5 {
		t.Errorf("the cut played backwards: %d frames match, want fewer than two thirds", got)
	}
}
