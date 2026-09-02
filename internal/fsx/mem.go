package fsx

import (
	"io"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/makearr/MediaNexus/internal/clock"
)

// maxSymlinkDepth matches the kernel's limit closely enough to make a loop an
// ELOOP rather than a hang.
const maxSymlinkDepth = 40

// Mem is an in-memory filesystem for tests (ADR-17). Import, linking, rename and
// path-safety logic run against it, so none of that needs a real disk.
//
// It models the failures that matter rather than only the happy path: reflink
// and hardlink support are switchable, because "this filesystem cannot clone"
// and "these two paths are on different filesystems" are the cases the import
// ladder (ADR-11) exists to handle and the ones production hits constantly.
type Mem struct {
	mu    sync.RWMutex
	clock clock.Clock
	nodes map[string]*memNode

	reflinkSupported  bool
	hardlinkSupported bool
}

type memNode struct {
	content    *memContent // nil for directories and symlinks
	mode       fs.FileMode
	isDir      bool
	linkTarget string // non-empty for a symlink

	// Each path keeps its own timestamp even when two of them share content,
	// because a hardlink is a directory entry, not a second file.
	modTime time.Time

	// Unowned (-1) to start, matching what OS reports when unavailable.
	uid int
	gid int
}

// memContent is shared between the two paths of a hardlink, which is what
// makes "wrote through one path, read through the other" testable.
type memContent struct {
	mu   sync.Mutex
	data []byte
}

// NewMem returns an empty filesystem containing only the root directory.
func NewMem(c clock.Clock) *Mem {
	m := &Mem{
		clock:             c,
		nodes:             make(map[string]*memNode),
		reflinkSupported:  true,
		hardlinkSupported: true,
	}
	m.nodes["/"] = &memNode{isDir: true, mode: fs.ModeDir | 0o755, modTime: m.now(), uid: -1, gid: -1}
	return m
}

// SetReflinkSupported switches block cloning on or off, standing in for a
// filesystem without it — OpenZFS below 2.2, for instance.
func (m *Mem) SetReflinkSupported(supported bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reflinkSupported = supported
}

// SetHardlinkSupported switches hardlinking on or off, standing in for two paths
// on different filesystems — separate ZFS datasets in one pool, or two NFS exports.
func (m *Mem) SetHardlinkSupported(supported bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hardlinkSupported = supported
}

func (m *Mem) now() time.Time { return m.clock.Now() }

func (m *Mem) Stat(path string) (FileInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	resolved, err := m.resolveLocked(path, 0)
	if err != nil {
		return FileInfo{}, pathError("stat", path, err)
	}
	return m.statLocked("stat", resolved)
}

func (m *Mem) Lstat(path string) (FileInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.statLocked("lstat", filepath.Clean(path))
}

func (m *Mem) statLocked(op, path string) (FileInfo, error) {
	node, ok := m.nodes[path]
	if !ok {
		return FileInfo{}, pathError(op, path, fs.ErrNotExist)
	}
	info := FileInfo{
		Name:    filepath.Base(path),
		Mode:    node.mode,
		ModTime: node.modTime,
		IsDir:   node.isDir,
		UID:     node.uid,
		GID:     node.gid,
	}
	if node.content != nil {
		node.content.mu.Lock()
		info.Size = int64(len(node.content.data))
		node.content.mu.Unlock()
	}
	return info, nil
}

func (m *Mem) Open(path string) (File, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	resolved, err := m.resolveLocked(path, 0)
	if err != nil {
		return nil, pathError("open", path, err)
	}
	node, ok := m.nodes[resolved]
	if !ok {
		return nil, pathError("open", path, fs.ErrNotExist)
	}
	if node.isDir {
		return nil, pathError("open", path, syscall.EISDIR)
	}
	return &memFile{content: node.content}, nil
}

func (m *Mem) Create(path string) (io.WriteCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	resolved, err := m.resolveLocked(path, 0)
	if err != nil {
		return nil, pathError("create", path, err)
	}
	if err := m.requireDir(filepath.Dir(resolved)); err != nil {
		return nil, pathError("create", path, err)
	}
	if existing, ok := m.nodes[resolved]; ok {
		if existing.isDir {
			return nil, pathError("create", path, syscall.EISDIR)
		}
		existing.content.mu.Lock()
		existing.content.data = existing.content.data[:0]
		existing.content.mu.Unlock()
		existing.modTime = m.now()
		return &memWriter{content: existing.content}, nil
	}

	node := &memNode{content: &memContent{}, mode: filePerm, modTime: m.now(), uid: -1, gid: -1}
	m.nodes[resolved] = node
	return &memWriter{content: node.content}, nil
}

// CreateSecret mirrors Create with the restricted mode, so the conformance
// suite can assert it on both implementations.
func (m *Mem) CreateSecret(path string) (io.WriteCloser, error) {
	writer, err := m.Create(path)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	resolved, err := m.resolveLocked(path, 0)
	if err != nil {
		return nil, err
	}
	if node, ok := m.nodes[resolved]; ok {
		node.mode = secretFilePerm
	}
	return writer, nil
}

// Chown records ownership; -1 leaves that half alone.
func (m *Mem) Chown(path string, uid, gid int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	resolved, err := m.resolveLocked(path, 0)
	if err != nil {
		return pathError("chown", path, err)
	}
	node, ok := m.nodes[resolved]
	if !ok {
		return pathError("chown", path, fs.ErrNotExist)
	}
	if uid >= 0 {
		node.uid = uid
	}
	if gid >= 0 {
		node.gid = gid
	}
	return nil
}

func (m *Mem) MkdirAll(path string, perm fs.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	resolved, err := m.resolveLocked(path, 0)
	if err != nil {
		return pathError("mkdir", path, err)
	}
	if !filepath.IsAbs(resolved) {
		return pathError("mkdir", path, ErrNotAbsolute)
	}

	current := "/"
	for _, component := range strings.Split(strings.Trim(resolved, "/"), "/") {
		if component == "" {
			continue
		}
		current = filepath.Join(current, component)
		node, ok := m.nodes[current]
		if ok {
			if !node.isDir {
				return pathError("mkdir", current, syscall.ENOTDIR)
			}
			continue
		}
		m.nodes[current] = &memNode{isDir: true, mode: fs.ModeDir | perm, modTime: m.now(), uid: -1, gid: -1}
	}
	return nil
}

func (m *Mem) ReadDir(path string) ([]DirEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	resolved, err := m.resolveLocked(path, 0)
	if err != nil {
		return nil, pathError("readdir", path, err)
	}
	if err := m.requireDir(resolved); err != nil {
		return nil, pathError("readdir", path, err)
	}

	prefix := strings.TrimSuffix(resolved, "/") + "/"
	var entries []DirEntry
	for candidate, node := range m.nodes {
		if !strings.HasPrefix(candidate, prefix) {
			continue
		}
		if strings.Contains(strings.TrimPrefix(candidate, prefix), "/") {
			continue // a grandchild, not a direct entry
		}
		entries = append(entries, DirEntry{Name: filepath.Base(candidate), IsDir: node.isDir})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries, nil
}

func (m *Mem) Rename(oldPath, newPath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	from := filepath.Clean(oldPath)
	to, err := m.resolveLocked(newPath, 0)
	if err != nil {
		return pathError("rename", newPath, err)
	}
	node, ok := m.nodes[from]
	if !ok {
		return pathError("rename", oldPath, fs.ErrNotExist)
	}
	if err := m.requireDir(filepath.Dir(to)); err != nil {
		return pathError("rename", newPath, err)
	}

	// Renaming a directory carries everything beneath it.
	if node.isDir {
		for candidate, moved := range m.nodes {
			if candidate == from || !strings.HasPrefix(candidate, from+"/") {
				continue
			}
			m.nodes[to+strings.TrimPrefix(candidate, from)] = moved
			delete(m.nodes, candidate)
		}
	}
	m.nodes[to] = node
	delete(m.nodes, from)
	return nil
}

func (m *Mem) Remove(path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	target := filepath.Clean(path)
	node, ok := m.nodes[target]
	if !ok {
		return pathError("remove", path, fs.ErrNotExist)
	}
	if node.isDir {
		for candidate := range m.nodes {
			if strings.HasPrefix(candidate, strings.TrimSuffix(target, "/")+"/") {
				return pathError("remove", path, syscall.ENOTEMPTY)
			}
		}
	}
	delete(m.nodes, target)
	return nil
}

func (m *Mem) Link(oldPath, newPath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.hardlinkSupported {
		return pathError("link", newPath, syscall.EXDEV)
	}
	source, err := m.resolveLocked(oldPath, 0)
	if err != nil {
		return pathError("link", oldPath, err)
	}
	node, ok := m.nodes[source]
	if !ok {
		return pathError("link", oldPath, fs.ErrNotExist)
	}
	if node.isDir {
		return pathError("link", oldPath, syscall.EPERM)
	}
	target := filepath.Clean(newPath)
	if err := m.newEntryLocked("link", target); err != nil {
		return err
	}
	// The shared content pointer is the hardlink: a write through either path is
	// visible from the other, exactly as an inode behaves.
	m.nodes[target] = &memNode{content: node.content, mode: node.mode, modTime: m.now(), uid: node.uid, gid: node.gid}
	return nil
}

func (m *Mem) Symlink(oldPath, newPath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	target := filepath.Clean(newPath)
	if err := m.newEntryLocked("symlink", target); err != nil {
		return err
	}
	m.nodes[target] = &memNode{linkTarget: oldPath, mode: fs.ModeSymlink | 0o777, modTime: m.now(), uid: -1, gid: -1}
	return nil
}

func (m *Mem) Reflink(oldPath, newPath string) error {
	m.mu.Lock()
	if !m.reflinkSupported {
		m.mu.Unlock()
		return ErrUnsupported
	}
	source, err := m.resolveLocked(oldPath, 0)
	if err != nil {
		m.mu.Unlock()
		return pathError("reflink", oldPath, err)
	}
	node, ok := m.nodes[source]
	if !ok || node.isDir {
		m.mu.Unlock()
		return pathError("reflink", oldPath, fs.ErrNotExist)
	}
	target := filepath.Clean(newPath)
	if err := m.newEntryLocked("reflink", target); err != nil {
		m.mu.Unlock()
		return err
	}

	node.content.mu.Lock()
	cloned := append([]byte(nil), node.content.data...)
	node.content.mu.Unlock()

	// A clone is copy-on-write, so the two paths hold equal but independent data.
	m.nodes[target] = &memNode{content: &memContent{data: cloned}, mode: node.mode, modTime: m.now(), uid: -1, gid: -1}
	m.mu.Unlock()
	return nil
}

func (m *Mem) Resolve(path string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	resolved, err := m.resolveLocked(path, 0)
	if err != nil {
		return "", pathError("resolve", path, err)
	}
	return resolved, nil
}

// resolveLocked expands symlinks in the existing part of path and keeps the rest
// verbatim, matching OS.Resolve so a test and production agree.
func (m *Mem) resolveLocked(path string, depth int) (string, error) {
	if depth > maxSymlinkDepth {
		return "", syscall.ELOOP
	}
	cleaned := filepath.Clean(path)
	if !filepath.IsAbs(cleaned) {
		return "", ErrNotAbsolute
	}

	walked := "/"
	components := strings.Split(strings.Trim(cleaned, "/"), "/")
	for i, component := range components {
		if component == "" {
			continue
		}
		next := filepath.Join(walked, component)
		node, ok := m.nodes[next]
		if !ok {
			return filepath.Join(append([]string{next}, components[i+1:]...)...), nil
		}
		if node.linkTarget != "" {
			target := node.linkTarget
			if !filepath.IsAbs(target) {
				target = filepath.Join(walked, target)
			}
			return m.resolveLocked(filepath.Join(append([]string{target}, components[i+1:]...)...), depth+1)
		}
		walked = next
	}
	return walked, nil
}

func (m *Mem) requireDir(path string) error {
	node, ok := m.nodes[path]
	if !ok {
		return fs.ErrNotExist
	}
	if !node.isDir {
		return syscall.ENOTDIR
	}
	return nil
}

// newEntryLocked checks that a path can be created: parent present, name free.
func (m *Mem) newEntryLocked(op, target string) error {
	if err := m.requireDir(filepath.Dir(target)); err != nil {
		return pathError(op, target, err)
	}
	if _, taken := m.nodes[target]; taken {
		return pathError(op, target, fs.ErrExist)
	}
	return nil
}

func pathError(op, path string, err error) error {
	return &fs.PathError{Op: op, Path: path, Err: err}
}
