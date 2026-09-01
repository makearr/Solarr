package fsx

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"path/filepath"
)

// tempPrefix marks a partial write. The name is distinctive so a crash leaves
// something obviously ours rather than something that looks like a media file.
const tempPrefix = ".mnx-tmp-"

// WriteAtomic writes through a temporary file in the destination's own directory
// and renames it into place, so an interrupted write leaves the library
// untouched. That is what makes the hard shutdown deadline in §15.2 safe.
//
// The temporary lives beside the destination, not in a temp directory, because a
// rename is only atomic within one filesystem.
func WriteAtomic(fsys FS, destination string, write func(io.Writer) error) error {
	return writeAtomic(fsys, destination, fsys.Create, write)
}

// WriteAtomicSecret is WriteAtomic for a file whose contents are a credential.
//
// The restricted mode is applied to the *temporary*, which is the only way to
// get it right: the temporary is what briefly holds the secret, so creating it
// world-readable and tightening the destination afterwards protects nothing.
// The rename carries the mode to the destination.
func WriteAtomicSecret(fsys FS, destination string, write func(io.Writer) error) error {
	return writeAtomic(fsys, destination, fsys.CreateSecret, write)
}

func writeAtomic(
	fsys FS,
	destination string,
	create func(string) (io.WriteCloser, error),
	write func(io.Writer) error,
) error {
	suffix, err := randomSuffix()
	if err != nil {
		return err
	}
	temporary := filepath.Join(filepath.Dir(destination), tempPrefix+filepath.Base(destination)+"."+suffix)

	file, err := create(temporary)
	if err != nil {
		return fmt.Errorf("create temporary for %q: %w", destination, err)
	}
	// The cleanup errors are deliberately dropped: the caller needs the reason
	// the write failed, and a failed removal of a temporary cannot corrupt the
	// destination, which was never touched.
	if err := write(file); err != nil {
		_ = file.Close()
		_ = fsys.Remove(temporary)
		return fmt.Errorf("write %q: %w", destination, err)
	}
	if err := file.Close(); err != nil {
		_ = fsys.Remove(temporary)
		return fmt.Errorf("close temporary for %q: %w", destination, err)
	}
	if err := fsys.Rename(temporary, destination); err != nil {
		_ = fsys.Remove(temporary)
		return fmt.Errorf("rename into %q: %w", destination, err)
	}
	return nil
}

// IsTemporary reports whether name is one of our partial writes. A scan skips
// them rather than trying to identify a half-written file.
func IsTemporary(name string) bool {
	return len(name) > len(tempPrefix) && name[:len(tempPrefix)] == tempPrefix
}

func randomSuffix() (string, error) {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate temporary name: %w", err)
	}
	return hex.EncodeToString(raw), nil
}
