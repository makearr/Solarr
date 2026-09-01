package fsx

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// filePerm is the mode for files MediaNexus creates. The process umask narrows
// it; the container runs non-root (§20.2).
//
// It is deliberately world-readable: imported media has to be readable by the
// media server running as a different user, which is the whole point of writing
// it. Do not tighten this to protect secrets -- see secretFilePerm.
const filePerm fs.FileMode = 0o644

// secretFilePerm is the mode for files whose contents are a credential: the
// config.yml holding the encryption key (ADR-14), and nothing that a media
// server ever needs to read.
//
// ADR-14 says there is no chmod *requirement* -- MediaNexus does not refuse to
// start on a loose mode, because that is a check that strands the user out of
// their own install. That is about what is enforced on a file we find. It says
// nothing about the mode we choose when we create one, and creating the only
// copy of a 256-bit key world-readable is indefensible when 0600 is free.
const secretFilePerm fs.FileMode = 0o600

// staleAttempts is how often a syscall is retried after ESTALE. An NFS server
// restart invalidates open handles, and treating that as a missing file would
// read as a deleted library (§11.2). The kernel re-resolves on the next call,
// so retrying immediately is enough; there is nothing to back off from.
const staleAttempts = 3

// OS is the production filesystem. It targets Linux, matching the deployment
// (§20.2) — Reflink in particular is a Linux ioctl.
type OS struct{}

// NewOS returns the production filesystem.
func NewOS() OS { return OS{} }

func (OS) Stat(path string) (FileInfo, error) {
	return retryStale(func() (FileInfo, error) {
		info, err := os.Stat(path)
		if err != nil {
			return FileInfo{}, err
		}
		return infoFrom(info), nil
	})
}

func (OS) Lstat(path string) (FileInfo, error) {
	return retryStale(func() (FileInfo, error) {
		info, err := os.Lstat(path)
		if err != nil {
			return FileInfo{}, err
		}
		return infoFrom(info), nil
	})
}

func (OS) Open(path string) (File, error) {
	return retryStale(func() (File, error) {
		return os.Open(path)
	})
}

func (OS) Create(path string) (io.WriteCloser, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, filePerm)
}

// CreateSecret creates the file already restricted, never chmod-ed afterwards:
// a create-then-chmod leaves a window in which the key is world-readable, and
// the window is exactly when the file has just been filled with the key.
func (OS) CreateSecret(path string) (io.WriteCloser, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, secretFilePerm)
}

func (OS) MkdirAll(path string, perm fs.FileMode) error { return os.MkdirAll(path, perm) }

// Chown is os.Chown. It is here rather than called directly so the forbidigo
// rule keeping every filesystem call inside this package stays absolute.
func (OS) Chown(path string, uid, gid int) error { return os.Chown(path, uid, gid) }
func (OS) Rename(oldPath, newPath string) error  { return os.Rename(oldPath, newPath) }
func (OS) Remove(path string) error              { return os.Remove(path) }
func (OS) Link(oldPath, newPath string) error    { return os.Link(oldPath, newPath) }
func (OS) Symlink(oldPath, newPath string) error { return os.Symlink(oldPath, newPath) }

func (OS) ReadDir(path string) ([]DirEntry, error) {
	return retryStale(func() ([]DirEntry, error) {
		read, err := os.ReadDir(path)
		if err != nil {
			return nil, err
		}
		entries := make([]DirEntry, 0, len(read))
		for _, entry := range read {
			entries = append(entries, DirEntry{Name: entry.Name(), IsDir: entry.IsDir()})
		}
		return entries, nil
	})
}

// Resolve expands symlinks in the part of path that exists and keeps the rest
// verbatim, so a destination can be checked for containment before it is created.
func (OS) Resolve(path string) (string, error) { return resolveExisting(path) }

func resolveExisting(path string) (string, error) {
	cleaned := filepath.Clean(path)
	resolved, err := filepath.EvalSymlinks(cleaned)
	if err == nil {
		return resolved, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}

	parent := filepath.Dir(cleaned)
	if parent == cleaned {
		return cleaned, nil // reached the filesystem root without finding it
	}
	resolvedParent, err := resolveExisting(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolvedParent, filepath.Base(cleaned)), nil
}

func infoFrom(info fs.FileInfo) FileInfo {
	converted := FileInfo{
		Name:    info.Name(),
		Size:    info.Size(),
		Mode:    info.Mode(),
		ModTime: info.ModTime(),
		IsDir:   info.IsDir(),
		UID:     -1,
		GID:     -1,
	}
	// Ownership is not part of fs.FileInfo. Where the platform reports it we
	// pass it through, because a permission error is nearly always a PUID/PGID
	// mismatch and section 14.3 promises to name the owner, not only the caller.
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		converted.UID = int(stat.Uid)
		converted.GID = int(stat.Gid)
	}
	return converted
}

func retryStale[T any](operation func() (T, error)) (T, error) {
	var result T
	var err error
	for attempt := 0; attempt < staleAttempts; attempt++ {
		result, err = operation()
		if err == nil || !errors.Is(err, syscall.ESTALE) {
			return result, err
		}
	}
	return result, err
}
