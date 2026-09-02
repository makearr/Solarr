package mediatool_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/makearr/MediaNexus/internal/mediatool"
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
