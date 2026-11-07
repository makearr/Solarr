package fsx

import (
	"errors"
	"io"
	"io/fs"
	"time"
)

// ErrReadOnly is a write refused because the path is mounted read-only by Solarr's mode, not by the system.
var ErrReadOnly = errors.New("read-only mode: this folder is not written to")

// Guarded is an FS whose writes under chosen paths are refused while Deny says so: libraries "mounted read-only".
type Guarded struct {
	FS
	// Deny returns an error to refuse a write to path, nil to let it through.
	Deny func(path string) error
}

// NewGuarded wraps inner; deny is consulted on every write.
func NewGuarded(inner FS, deny func(path string) error) Guarded {
	return Guarded{FS: inner, Deny: deny}
}

func (g Guarded) refuse(op string, paths ...string) error {
	for _, path := range paths {
		if err := g.Deny(path); err != nil {
			return &fs.PathError{Op: op, Path: path, Err: err}
		}
	}
	return nil
}

// Create refuses a write under a read-only path.
func (g Guarded) Create(path string) (io.WriteCloser, error) {
	if err := g.refuse("create", path); err != nil {
		return nil, err
	}
	return g.FS.Create(path)
}

// CreateSecret refuses a write under a read-only path.
func (g Guarded) CreateSecret(path string) (io.WriteCloser, error) {
	if err := g.refuse("create", path); err != nil {
		return nil, err
	}
	return g.FS.CreateSecret(path)
}

// MkdirAll refuses a write under a read-only path.
func (g Guarded) MkdirAll(path string, perm fs.FileMode) error {
	if err := g.refuse("mkdir", path); err != nil {
		return err
	}
	return g.FS.MkdirAll(path, perm)
}

// Rename refuses when either end is under a read-only path.
func (g Guarded) Rename(oldPath, newPath string) error {
	if err := g.refuse("rename", oldPath, newPath); err != nil {
		return err
	}
	return g.FS.Rename(oldPath, newPath)
}

// Remove refuses a delete under a read-only path.
func (g Guarded) Remove(path string) error {
	if err := g.refuse("remove", path); err != nil {
		return err
	}
	return g.FS.Remove(path)
}

// Chown refuses a change under a read-only path.
func (g Guarded) Chown(path string, uid, gid int) error {
	if err := g.refuse("chown", path); err != nil {
		return err
	}
	return g.FS.Chown(path, uid, gid)
}

// Chmod refuses a change under a read-only path.
func (g Guarded) Chmod(path string, mode fs.FileMode) error {
	if err := g.refuse("chmod", path); err != nil {
		return err
	}
	return g.FS.Chmod(path, mode)
}

// Chtimes refuses a change under a read-only path.
func (g Guarded) Chtimes(path string, modTime time.Time) error {
	if err := g.refuse("chtimes", path); err != nil {
		return err
	}
	return g.FS.Chtimes(path, modTime)
}

// Link refuses a new name under a read-only path.
func (g Guarded) Link(oldPath, newPath string) error {
	if err := g.refuse("link", newPath); err != nil {
		return err
	}
	return g.FS.Link(oldPath, newPath)
}

// Symlink refuses a new name under a read-only path.
func (g Guarded) Symlink(oldPath, newPath string) error {
	if err := g.refuse("symlink", newPath); err != nil {
		return err
	}
	return g.FS.Symlink(oldPath, newPath)
}

// Reflink refuses a new file under a read-only path.
func (g Guarded) Reflink(oldPath, newPath string) error {
	if err := g.refuse("reflink", newPath); err != nil {
		return err
	}
	return g.FS.Reflink(oldPath, newPath)
}
