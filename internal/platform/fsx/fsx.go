// Package fsx is the filesystem seam (ADR-17) and the single choke point for path safety
// (ADR-14, §11.2, Invariant 20).
package fsx

import (
	"errors"
	"io"
	"io/fs"
	"time"
)

var (
	// ErrUnsupported reports an operation this filesystem cannot perform, such as a reflink on a
	// filesystem without block cloning (ADR-11).
	ErrUnsupported = errors.New("fsx: operation not supported by this filesystem")

	// ErrEscapesRoot reports a path that resolved outside the root folder it was
	// built beneath. It is always a hard error and never retried (Invariant 20).
	ErrEscapesRoot = errors.New("fsx: path escapes its root folder")

	// ErrEmptyComponent reports a path component that sanitised away to nothing,
	// which would silently move a file up a directory level.
	ErrEmptyComponent = errors.New("fsx: path component is empty after sanitising")

	// ErrNotAbsolute reports a root folder given as a relative path. Roots come
	// from configuration and are validated at startup (§14.3), so this is a bug.
	ErrNotAbsolute = errors.New("fsx: root must be an absolute path")
)

// FileInfo is the subset of stat Solarr uses. It is a value type rather than
// os.FileInfo so an in-memory filesystem does not have to impersonate one.
type FileInfo struct {
	Name    string
	Size    int64
	Mode    fs.FileMode
	ModTime time.Time
	IsDir   bool
	// -1 where unreported, so "not known" differs from "owned by root".
	UID int
	GID int
}

// DirEntry carries only what a directory read yields for free (ADR-22).
type DirEntry struct {
	Name  string
	IsDir bool
}

// File is an open file. Seeking is required, not incidental: oshash reads the
// head and the tail of a file and nothing in between (ADR-35).
type File interface {
	io.ReadCloser
	io.Seeker
	io.ReaderAt
}

// FS is the filesystem.
type FS interface {
	Stat(path string) (FileInfo, error)
	Lstat(path string) (FileInfo, error)
	Open(path string) (File, error)
	Create(path string) (io.WriteCloser, error)

	// CreateSecret is Create for a credential. A separate method, not a mode
	// argument, so the restricted case is named at every call site.
	CreateSecret(path string) (io.WriteCloser, error)
	MkdirAll(path string, perm fs.FileMode) error
	ReadDir(path string) ([]DirEntry, error)
	Rename(oldPath, newPath string) error
	Remove(path string) error

	// Chown changes ownership; -1 leaves that half alone, as chown(2) does.
	Chown(path string, uid, gid int) error

	// Link creates a hardlink. Preferred for both import and cross-entry linking
	// (ADR-11); fails with ErrUnsupported across filesystems.
	Link(oldPath, newPath string) error

	// Symlink is never used by import (Invariant 19) and only by explicit user
	// choice for cross-entry linking.
	Symlink(oldPath, newPath string) error

	// Reflink is a copy-on-write clone. Returns ErrUnsupported wherever block
	// cloning is unavailable, which includes OpenZFS below 2.2.
	Reflink(oldPath, newPath string) error

	// Resolve returns path with all symlinks expanded. Components that do not
	// exist yet are kept as given, so a path can be resolved before it is created.
	Resolve(path string) (string, error)

	// DiskUsage reports the filesystem holding path's total and free bytes
	// (section 11.7: capacity is polled, not assumed). free is what is
	// actually available to this process, not raw free blocks -- root's
	// reserved blocks are not this process's to place into.
	DiskUsage(path string) (totalBytes, freeBytes int64, err error)
}
