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
	if err := sidecar.HandOverSidecars(t.Context(), fsys, "/lib/Show/Season 1/Show - S01E01.mp4", "/lib/Show/Season 1/Show - S01E01 [upgrade].mkv"); err != nil {
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

// "Film.2010.Extended.en.srt" belongs to the Extended cut, not to "Film.2010".
func TestHandOverSidecarsLeavesAnotherVersionsSubtitles(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	write := func(name string) {
		if err := fsys.MkdirAll("/lib/Film", 0o755); err != nil {
			t.Fatal(err)
		}
		file, _ := fsys.Create("/lib/Film/" + name)
		_, _ = file.Write([]byte(name))
		_ = file.Close()
	}
	for _, name := range []string{"Film.2010.mkv", "Film.2010.en.forced.srt", "Film.2010.Extended.mkv", "Film.2010.Extended.en.srt"} {
		write(name)
	}

	if err := sidecar.HandOverSidecars(t.Context(), fsys, "/lib/Film/Film.2010.mkv", "/lib/Film/Film.2010 [upgrade].mkv"); err != nil {
		t.Fatal(err)
	}

	for name, want := range map[string]bool{
		"Film.2010 [upgrade].en.forced.srt": true, "Film.2010.en.forced.srt": false,
		"Film.2010.Extended.en.srt": true, "Film.2010 [upgrade].Extended.en.srt": false,
	} {
		if _, err := fsys.Stat("/lib/Film/" + name); (err == nil) != want {
			t.Errorf("%s present = %v, want %v", name, err == nil, want)
		}
	}
}

// Across filesystems a rename fails; the subtitle still arrives, and one failure does not stop the next.
func TestHandOverSidecarsCopesWithARenameThatCrossesFilesystems(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	fsys.SetRenameSupported(false)
	for _, directory := range []string{"/old/Film", "/new/Film"} {
		if err := fsys.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"Film.mkv", "Film.en.srt", "Film.de.srt"} {
		file, _ := fsys.Create("/old/Film/" + name)
		_, _ = file.Write([]byte(name))
		_ = file.Close()
	}

	if err := sidecar.HandOverSidecars(t.Context(), fsys, "/old/Film/Film.mkv", "/new/Film/Film.mkv"); err != nil {
		t.Fatalf("HandOverSidecars: %v", err)
	}
	for _, name := range []string{"Film.en.srt", "Film.de.srt"} {
		if _, err := fsys.Stat("/new/Film/" + name); err != nil {
			t.Errorf("%s did not arrive: %v", name, err)
		}
		if _, err := fsys.Stat("/old/Film/" + name); err == nil {
			t.Errorf("%s was left behind", name)
		}
	}
}
