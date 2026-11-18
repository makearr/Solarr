package archive_test

import (
	"fmt"
	"testing"

	"github.com/makearr/Solarr/internal/platform/archive"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

func writeSized(t *testing.T, fsys *fsx.Mem, name string, size int) {
	t.Helper()
	writer, err := fsys.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(make([]byte, size)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
}

// The files named after the set (its .nfo) count with it, as they are removed with it; another release does not.
func TestVolumesSize_SumsEveryVolumeOfTheSetAndTheFilesNamedAfterIt(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	if err := fsys.MkdirAll("/d", 0o755); err != nil {
		t.Fatal(err)
	}
	for volume := 1; volume <= 300; volume++ {
		writeSized(t, fsys, fmt.Sprintf("/d/Release.part%03d.rar", volume), 1024)
	}
	writeSized(t, fsys, "/d/Other.part001.rar", 5000)
	writeSized(t, fsys, "/d/Release.nfo", 77)
	writeSized(t, fsys, "/d/Single.zip", 900)

	if got := archive.VolumesSize(fsys, "/d/Release.part001.rar", 1024); got != 300*1024+77 {
		t.Errorf("a set of 300 volumes = %d, want %d", got, 300*1024+77)
	}
	if got := archive.VolumesSize(fsys, "/d/Single.zip", 900); got != 900 {
		t.Errorf("a single archive = %d, want its own size", got)
	}
	if got := archive.VolumesSize(fsys, "/nowhere/A.part001.rar", 42); got != 42 {
		t.Errorf("an unlistable directory = %d, want the primary's size", got)
	}
}
