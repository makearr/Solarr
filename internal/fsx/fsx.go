// Package fsx is the filesystem seam (ADR-17) and the single choke point for
// path safety (ADR-14, §11.2). Every filesystem write in the process passes
// through it, so callers are never trusted to validate a path themselves — the
// containment check in SafeJoin is what makes Invariant 20 structural rather
// than a rule each caller must remember.
package fsx

import (
	"errors"
	"io"
	"io/fs"
	"time"
)

var (
	// ErrUnsupported reports an operation this filesystem cannot perform, such as
	// a reflink on a filesystem without block cloning. It is not a failure: the
	// import ladder (reflink → hardlink → copy, ADR-11) treats it as "try the next".
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

// FileInfo is the subset of stat MediaNexus uses. It is a value type rather than
// os.FileInfo so an in-memory filesystem does not have to impersonate one.
type FileInfo struct {
	Name    string
	Size    int64
	Mode    fs.FileMode
	ModTime time.Time
	IsDir   bool
	// UID and GID are -1 where the platform or implementation does not report
	// them, so a caller can tell "not known" from "owned by root".
	UID int
	GID int
}

// DirEntry carries only what a directory read yields for free. Size and ModTime
// cost a stat per entry on Linux, which at 500k files (ADR-22) is the difference
// between a sweep and an outage, so callers ask for them explicitly.
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

// FS is the filesystem. Implementations are OS (production) and Mem (tests).
//
// Paths given to these methods are already-resolved absolute paths — SafeJoin
// produces them. The methods do not sanitise, because a check that happens in
// two places happens correctly in one.
type FS interface {
	Stat(path string) (FileInfo, error)
	Lstat(path string) (FileInfo, error)
	Open(path string) (File, error)
	Create(path string) (io.WriteCloser, error)

	// CreateSecret is Create for a file whose contents are a credential. It is a
	// separate method rather than a mode argument so that the restricted case is
	// named at every call site and cannot be reached by passing the wrong
	// constant.
	CreateSecret(path string) (io.WriteCloser, error)
	MkdirAll(path string, perm fs.FileMode) error
	ReadDir(path string) ([]DirEntry, error)
	Rename(oldPath, newPath string) error
	Remove(path string) error

	// Chown changes ownership. Used when the container starts as root to hand
	// its managed directories to the runtime user (PUID/PGID) before dropping
	// privileges. A uid or gid of -1 leaves that half unchanged, as chown(2) does.
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
}
