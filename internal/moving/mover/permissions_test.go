package mover_test

import (
	"io/fs"
	"testing"

	"github.com/makearr/Solarr/internal/moving/mover"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// Everything from the file up to the root changes; the root itself is the host's and does not.
func TestApplyPermissions_StopsAtTheRoot(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	if err := fsys.MkdirAll("/library/Heka/Zanuvode (1988)", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := fsys.Chmod("/library", 0o700); err != nil {
		t.Fatal(err)
	}
	file, err := fsys.Create("/library/Heka/Zanuvode (1988)/03.flac")
	if err != nil {
		t.Fatal(err)
	}
	_ = file.Close()

	err = mover.ApplyPermissions(fsys, "/library/", "/library/Heka/Zanuvode (1988)/03.flac",
		mover.Permissions{FolderMode: 0o775, FileMode: 0o664, GID: 1000})
	if err != nil {
		t.Fatalf("ApplyPermissions: %v", err)
	}
	want := map[string]fs.FileMode{
		"/library": 0o700, "/library/Heka": 0o775, "/library/Heka/Zanuvode (1988)": 0o775,
		"/library/Heka/Zanuvode (1988)/03.flac": 0o664,
	}
	for target, mode := range want {
		info, err := fsys.Stat(target)
		if err != nil {
			t.Fatalf("Stat(%q): %v", target, err)
		}
		wantGID := 1000
		if target == "/library" {
			wantGID = -1
		}
		if info.Mode.Perm() != mode || info.GID != wantGID {
			t.Errorf("%s: mode %04o group %d, want %04o group %d", target, info.Mode.Perm(), info.GID, mode, wantGID)
		}
	}
}
