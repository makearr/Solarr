package fsx_test

import (
	"errors"
	"io"
	"io/fs"
	"strings"
	"syscall"
	"testing"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/fsx"
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
