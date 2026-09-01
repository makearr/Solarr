package fsx_test

import (
	"errors"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/makearr/MediaNexus/internal/fsx"
)

// Reflink support is a property of the filesystem under the test runner, which
// is overlayfs in CI and could be anything locally. Either answer is correct;
// what must hold in both cases is that a refused clone leaves nothing behind,
// because the import ladder falls through to hardlink and would find the
// destination already taken (ADR-11).
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
