package fsx_test

import (
	"errors"
	"io"
	"io/fs"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// The import ladder is reflink → hardlink → copy (ADR-11), so the fake has to be able to say no
// to each rung.

func TestMem_ReflinkClonesIndependently(t *testing.T) {
	fsys, root := newMemUnderTest(t)
	original, cloned := root+"/a.flac", root+"/b.flac"
	writeFile(t, fsys, original, "first")

	if err := fsys.Reflink(original, cloned); err != nil {
		t.Fatalf("Reflink: %v", err)
	}
	if got := readFile(t, fsys, cloned); got != "first" {
		t.Errorf("clone reads %q, want %q", got, "first")
	}

	// Copy-on-write: unlike a hardlink, a rewrite of one does not touch the other.
	writeFile(t, fsys, original, "second")
	if got := readFile(t, fsys, cloned); got != "first" {
		t.Errorf("clone changed with the original: %q, want %q", got, "first")
	}
}

func TestMem_ReflinkReportsUnsupportedWhenSwitchedOff(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	if err := fsys.MkdirAll("/library", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeFile(t, fsys, "/library/a.flac", "audio")
	fsys.SetReflinkSupported(false)

	err := fsys.Reflink("/library/a.flac", "/library/b.flac")
	if !errors.Is(err, fsx.ErrUnsupported) {
		t.Fatalf("Reflink = %v, want ErrUnsupported", err)
	}
	if _, err := fsys.Stat("/library/b.flac"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("an unsupported reflink left a destination behind: %v", err)
	}
}

func TestMem_LinkAcrossFilesystemsIsEXDEV(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	if err := fsys.MkdirAll("/library", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeFile(t, fsys, "/library/a.flac", "audio")
	fsys.SetHardlinkSupported(false)

	if err := fsys.Link("/library/a.flac", "/library/b.flac"); !errors.Is(err, syscall.EXDEV) {
		t.Errorf("Link = %v, want EXDEV — two ZFS datasets in one pool behave this way", err)
	}
}

func TestMem_RenameAcrossFilesystemsIsEXDEV(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	if err := fsys.MkdirAll("/library", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeFile(t, fsys, "/library/a.flac", "audio")
	fsys.SetRenameSupported(false)

	if err := fsys.Rename("/library/a.flac", "/library/b.flac"); !errors.Is(err, syscall.EXDEV) {
		t.Errorf("Rename = %v, want EXDEV — recycle bin's own cross-device case (section 10.8)", err)
	}
	if _, err := fsys.Stat("/library/a.flac"); err != nil {
		t.Errorf("a failed rename must leave the source alone: %v", err)
	}
}

func TestMem_SymlinkLoopIsELOOPNotAHang(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	if err := fsys.MkdirAll("/library", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := fsys.Symlink("/library/b", "/library/a"); err != nil {
		t.Fatalf("Symlink a: %v", err)
	}
	if err := fsys.Symlink("/library/a", "/library/b"); err != nil {
		t.Fatalf("Symlink b: %v", err)
	}

	if _, err := fsys.Resolve("/library/a"); !errors.Is(err, syscall.ELOOP) {
		t.Errorf("Resolve of a symlink loop = %v, want ELOOP", err)
	}
}

func TestMem_RenameCarriesADirectorySubtree(t *testing.T) {
	fsys, root := newMemUnderTest(t)
	if err := fsys.MkdirAll(root+"/Old Artist/Album", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeFile(t, fsys, root+"/Old Artist/Album/track.flac", "audio")

	if err := fsys.Rename(root+"/Old Artist", root+"/New Artist"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if got := readFile(t, fsys, root+"/New Artist/Album/track.flac"); got != "audio" {
		t.Errorf("moved file = %q, want %q", got, "audio")
	}
	if _, err := fsys.Stat(root + "/Old Artist/Album/track.flac"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the old subtree survived the rename: %v", err)
	}
}

func TestMem_CreateBeneathAMissingDirectoryFails(t *testing.T) {
	fsys, root := newMemUnderTest(t)
	if _, err := fsys.Create(root + "/absent/track.flac"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Create = %v, want ErrNotExist; MkdirAll is the caller's job", err)
	}
}

func TestMem_ModificationTimeComesFromTheInjectedClock(t *testing.T) {
	fake := clock.NewFakeAtEpoch()
	fsys := fsx.NewMem(fake)
	if err := fsys.MkdirAll("/library", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeFile(t, fsys, "/library/track.flac", "audio")

	info, err := fsys.Stat("/library/track.flac")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !info.ModTime.Equal(fake.Now()) {
		t.Errorf("ModTime = %v, want the fake clock's %v", info.ModTime, fake.Now())
	}
}

func TestWriteAtomic_PublishesOnlyOnSuccess(t *testing.T) {
	fsys, root := newMemUnderTest(t)
	destination := root + "/album.nfo"

	err := fsx.WriteAtomic(fsys, destination, func(w io.Writer) error {
		_, writeErr := io.WriteString(w, "<album/>")
		return writeErr
	})
	if err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	if got := readFile(t, fsys, destination); got != "<album/>" {
		t.Errorf("destination = %q, want %q", got, "<album/>")
	}

	entries, err := fsys.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		if fsx.IsTemporary(entry.Name) {
			t.Errorf("a successful write left the temporary %q behind", entry.Name)
		}
	}
}

func TestWriteAtomic_TemporaryLivesBesideTheDestination(t *testing.T) {
	fsys, root := newMemUnderTest(t)
	if err := fsys.MkdirAll(root+"/Artist/Album", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	var temporaryDirectory string
	err := fsx.WriteAtomic(fsys, root+"/Artist/Album/album.nfo", func(io.Writer) error {
		// A rename is atomic only within one filesystem, so the temporary must
		// share a directory with the destination, not live in /tmp.
		entries, readErr := fsys.ReadDir(root + "/Artist/Album")
		if readErr != nil {
			return readErr
		}
		for _, entry := range entries {
			if fsx.IsTemporary(entry.Name) {
				temporaryDirectory = root + "/Artist/Album"
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	if temporaryDirectory == "" {
		t.Error("no temporary was created in the destination's own directory")
	}
}

func TestMem_ReadDirSkipsGrandchildren(t *testing.T) {
	fsys, root := newMemUnderTest(t)
	if err := fsys.MkdirAll(root+"/Artist/Album/Disc 1", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	entries, err := fsys.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "Artist" {
		names := make([]string, len(entries))
		for i, entry := range entries {
			names[i] = entry.Name
		}
		t.Errorf("ReadDir = %v, want only [Artist]", strings.Join(names, ", "))
	}
}

// The section 10.4 sweep decides whether to open a directory from its mtime, so the
// fake has to move it for the same reasons a real filesystem does. os_test.go holds
// the matching assertion against the real one.
func TestMem_ChangingADirectorysEntriesMovesItsModificationTime(t *testing.T) {
	fake := clock.NewFakeAtEpoch()
	fsys := fsx.NewMem(fake)
	if err := fsys.MkdirAll("/library/album", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	modTimeOf := func(path string) time.Time {
		t.Helper()
		info, err := fsys.Stat(path)
		if err != nil {
			t.Fatalf("Stat(%q): %v", path, err)
		}
		return info.ModTime
	}

	for _, step := range []struct {
		name   string
		change func()
	}{
		{"creating a file", func() { writeMem(t, fsys, "/library/album/01.flac") }},
		{"creating a subdirectory", func() {
			if err := fsys.MkdirAll("/library/album/scans", 0o755); err != nil {
				t.Fatalf("MkdirAll: %v", err)
			}
		}},
		{"renaming a file in", func() {
			if err := fsys.Rename("/library/album/01.flac", "/library/album/02.flac"); err != nil {
				t.Fatalf("Rename: %v", err)
			}
		}},
		{"removing a file", func() {
			if err := fsys.Remove("/library/album/02.flac"); err != nil {
				t.Fatalf("Remove: %v", err)
			}
		}},
	} {
		before := modTimeOf("/library/album")
		fake.Advance(time.Second)
		step.change()
		if after := modTimeOf("/library/album"); !after.After(before) {
			t.Errorf("%s left the directory's mtime at %s", step.name, after)
		}
	}

	// Rewriting a file changes the file, not the listing, so a real filesystem
	// leaves the directory alone -- and the sweep's stat-verification layer, not
	// the mtime sweep, is what catches it (section 10.4).
	writeMem(t, fsys, "/library/album/03.flac")
	before := modTimeOf("/library/album")
	fake.Advance(time.Second)
	writeMem(t, fsys, "/library/album/03.flac")
	if after := modTimeOf("/library/album"); after.After(before) {
		t.Errorf("rewriting a file moved the directory's mtime to %s", after)
	}
}

func TestMem_DiskUsageDefaultsUntilSet(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	total, free, err := fsys.DiskUsage("/library")
	if err != nil {
		t.Fatalf("DiskUsage: %v", err)
	}
	if total <= 0 || free <= 0 {
		t.Errorf("total=%d free=%d, want both positive defaults", total, free)
	}
}

func TestMem_SetDiskUsageOverridesTheDefault(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	fsys.SetDiskUsage(2_000, 500)

	total, free, err := fsys.DiskUsage("/library")
	if err != nil {
		t.Fatalf("DiskUsage: %v", err)
	}
	if total != 2_000 || free != 500 {
		t.Errorf("DiskUsage = (%d, %d), want (2000, 500)", total, free)
	}
}

func writeMem(t *testing.T, fsys fsx.FS, path string) {
	t.Helper()
	file, err := fsys.Create(path)
	if err != nil {
		t.Fatalf("Create(%q): %v", path, err)
	}
	if _, err := file.Write([]byte("bytes")); err != nil {
		t.Fatalf("Write(%q): %v", path, err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close(%q): %v", path, err)
	}
}
