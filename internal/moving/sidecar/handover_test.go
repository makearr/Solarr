package sidecar_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/moving/sidecar"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// The subtitles of a replaced episode follow the file that replaces it, and never overwrite the new file's own.
func TestHandOverSidecarsFollowsTheReplacingFile(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	write := func(name string) {
		if err := fsys.MkdirAll("/lib/Show/Season 1", 0o755); err != nil {
			t.Fatal(err)
		}
		file, err := fsys.Create("/lib/Show/Season 1/" + name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = file.Write([]byte(name))
		_ = file.Close()
	}
	for _, name := range []string{"Show - S01E01.mp4", "Show - S01E01.en.srt", "Show - S01E01.de.srt", "Show - S01E01.nfo", "Show - S01E01 [upgrade].de.srt"} {
		write(name)
	}
	if err := sidecar.HandOverSidecars(fsys, "/lib/Show/Season 1/Show - S01E01.mp4", "/lib/Show/Season 1/Show - S01E01 [upgrade].mkv"); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{
		"Show - S01E01 [upgrade].en.srt": true, "Show - S01E01.en.srt": false, "Show - S01E01.nfo": true,
		"Show - S01E01 [upgrade].de.srt": true, "Show - S01E01.de.srt": true,
	} {
		_, err := fsys.Stat("/lib/Show/Season 1/" + name)
		if (err == nil) != want {
			t.Errorf("%s present = %v, want %v", name, err == nil, want)
		}
	}
}
