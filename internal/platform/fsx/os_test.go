package fsx_test

import (
	"errors"
	"io/fs"
	"path/filepath"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/platform/fsx"
)

// Reflink support is a property of the filesystem under the test runner, which is overlayfs in
// CI and could be anything locally (ADR-11).
func TestOS_ReflinkEitherClonesOrLeavesNothingBehind(t *testing.T) {
	fsys := fsx.NewOS()
	root, err := fsys.Resolve(t.TempDir())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	original, cloned := filepath.Join(root, "a.flac"), filepath.Join(root, "b.flac")
	writeFile(t, fsys, original, "audio")

	switch err := fsys.Reflink(original, cloned); {
	case err == nil:
		if got := readFile(t, fsys, cloned); got != "audio" {
			t.Errorf("clone = %q, want %q", got, "audio")
		}
	case errors.Is(err, fsx.ErrUnsupported):
		if _, statErr := fsys.Stat(cloned); !errors.Is(statErr, fs.ErrNotExist) {
			t.Errorf("an unsupported reflink left %q behind: %v", cloned, statErr)
		}
		t.Logf("this filesystem has no block cloning, which is the common case")
	default:
		t.Fatalf("Reflink = %v, want nil or ErrUnsupported", err)
	}
}

func TestOS_ResolveReportsALoopRatherThanHanging(t *testing.T) {
	fsys := fsx.NewOS()
	root, err := fsys.Resolve(t.TempDir())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	first, second := filepath.Join(root, "a"), filepath.Join(root, "b")
	if err := fsys.Symlink(second, first); err != nil {
		t.Fatalf("Symlink a: %v", err)
	}
	if err := fsys.Symlink(first, second); err != nil {
		t.Fatalf("Symlink b: %v", err)
	}

	if _, err := fsys.Resolve(first); err == nil {
		t.Error("Resolve of a symlink loop succeeded; it must report ELOOP")
	}
}

// What the fake is imitating. Mem's matching test asserts the same rules, and this
// is what says they are the real filesystem's rules and not an invention.
func TestOS_ChangingADirectorysEntriesMovesItsModificationTime(t *testing.T) {
	fsys := fsx.NewOS()
	directory := t.TempDir()
	modTimeOf := func() time.Time {
		t.Helper()
		info, err := fsys.Stat(directory)
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}
		return info.ModTime
	}

	first := filepath.Join(directory, "01.flac")
	before := modTimeOf()
	writeOS(t, fsys, first)
	created := modTimeOf()
	if !created.After(before) {
		t.Fatalf("creating a file left the mtime at %s", created)
	}

	if err := fsys.Remove(first); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if removed := modTimeOf(); !removed.After(created) {
		t.Errorf("removing a file left the mtime at %s", removed)
	}
}

// TestOS_DiskUsageReportsSaneValues cannot assert an exact figure -- the test
// runner's own disk is whatever it is -- but total and free must both be
// positive and free must never exceed total, which a bad statfs field
// mapping (blocks vs bytes, Bfree vs Bavail) would violate immediately.
func TestOS_DiskUsageReportsSaneValues(t *testing.T) {
	fsys := fsx.NewOS()
	total, free, err := fsys.DiskUsage(t.TempDir())
	if err != nil {
		t.Fatalf("DiskUsage: %v", err)
	}
	if total <= 0 {
		t.Errorf("total = %d, want > 0", total)
	}
	if free < 0 {
		t.Errorf("free = %d, want >= 0", free)
	}
	if free > total {
		t.Errorf("free = %d exceeds total = %d", free, total)
	}
}

func writeOS(t *testing.T, fsys fsx.FS, path string) {
	t.Helper()
	file, err := fsys.Create(path)
	if err != nil {
		t.Fatalf("Create(%q): %v", path, err)
	}
	if _, err := file.Write([]byte("bytes")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
