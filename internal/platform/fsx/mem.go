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

	"github.com/makearr/Solarr/internal/platform/clock"
)

// maxSymlinkDepth matches the kernel's limit closely enough to make a loop an
// ELOOP rather than a hang.
const maxSymlinkDepth = 40

// Mem is an in-memory filesystem for tests (ADR-17).
type Mem struct {
	mu    sync.RWMutex
	clock clock.Clock
	nodes map[string]*memNode

	reflinkSupported  bool
	hardlinkSupported bool
	renameSupported   bool
	writeDenied       map[string]bool

	nextInode      uint64
	diskTotalBytes int64
	diskFreeBytes  int64
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
	inode uint64
	mu    sync.Mutex
	data  []byte
}

// NewMem returns an empty filesystem containing only the root directory.
// defaultDiskTotalBytes and defaultDiskFreeBytes are arbitrary but large
// enough that an unconfigured test never mistakes the fake for a full disk;
// SetDiskUsage overrides them for a test that cares about a specific value.
const (
	defaultDiskTotalBytes = 1 << 40 // 1 TiB
	defaultDiskFreeBytes  = 1 << 39 // 512 GiB
)

// NewMem returns the in-memory fake, rooted at "/".
func NewMem(c clock.Clock) *Mem {
	m := &Mem{
		clock:             c,
		nodes:             make(map[string]*memNode),
		reflinkSupported:  true,
		hardlinkSupported: true,
		renameSupported:   true,
		diskTotalBytes:    defaultDiskTotalBytes,
		diskFreeBytes:     defaultDiskFreeBytes,
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

// SetDiskUsage stands in for statfs(2): there is no real disk backing a Mem,
// so a test that cares about a specific total/free reading sets it here.
func (m *Mem) SetDiskUsage(totalBytes, freeBytes int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.diskTotalBytes, m.diskFreeBytes = totalBytes, freeBytes
}

// DiskUsage returns whatever SetDiskUsage last set, or the default if it was
// never called -- path is unused, since a Mem simulates exactly one
// filesystem regardless of how many paths are asked about.
func (m *Mem) DiskUsage(string) (totalBytes, freeBytes int64, err error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.diskTotalBytes, m.diskFreeBytes, nil
}

// FilesystemID is one id for the whole in-memory filesystem.
func (m *Mem) FilesystemID(string) (string, error) { return "mem", nil }

// SetHardlinkSupported switches hardlinking on or off, standing in for two paths
// on different filesystems — separate ZFS datasets in one pool, or two NFS exports.
func (m *Mem) SetHardlinkSupported(supported bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hardlinkSupported = supported
}

// SetRenameSupported switches Rename's own cross-device behaviour off, standing
// in for the same "two different filesystems" case Link already simulates --
// section 10.8's recycle bin refuses to cross one rather than silently copying.
func (m *Mem) SetRenameSupported(supported bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.renameSupported = supported
}

// SetWriteDenied stands in for a directory this process cannot write to --
// wrong PUID/PGID against the volume's real owner, the ordinary case
// Invariant 62 exists for. Mem otherwise never enforces permission bits.
func (m *Mem) SetWriteDenied(directory string, denied bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.writeDenied == nil {
		m.writeDenied = make(map[string]bool)
	}
	m.writeDenied[filepath.Clean(directory)] = denied
}

func (m *Mem) now() time.Time { return m.clock.Now() }

// newInode numbers a new content; callers hold m.mu for writing.
func (m *Mem) newInode() uint64 {
	m.nextInode++
	return m.nextInode
}

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
	cleaned := filepath.Clean(path)
	parent, err := m.resolveLocked(filepath.Dir(cleaned), 0)
	if err != nil {
		return FileInfo{}, pathError("lstat", path, err)
	}
	return m.statLocked("lstat", filepath.Join(parent, filepath.Base(cleaned)))
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
		info.Inode = node.content.inode
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
	if m.writeDenied[filepath.Dir(resolved)] {
		return nil, pathError("create", path, syscall.EACCES)
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

	node := &memNode{content: &memContent{inode: m.newInode()}, mode: filePerm, modTime: m.now(), uid: -1, gid: -1}
	m.nodes[resolved] = node
	m.touchParent(resolved)
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

// Chmod records the permission bits.
func (m *Mem) Chmod(path string, mode fs.FileMode) error {
	return m.updateNode("chmod", path, func(node *memNode) { node.mode = node.mode.Type() | mode.Perm() })
}

// Chtimes records the modification time.
func (m *Mem) Chtimes(path string, modTime time.Time) error {
	return m.updateNode("chtimes", path, func(node *memNode) { node.modTime = modTime })
}

func (m *Mem) updateNode(op, path string, update func(*memNode)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	resolved, err := m.resolveLocked(path, 0)
	if err != nil {
		return pathError(op, path, err)
	}
	node, ok := m.nodes[resolved]
	if !ok {
		return pathError(op, path, fs.ErrNotExist)
	}
	update(node)
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
		m.touchParent(current)
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
	if !m.renameSupported {
		return pathError("rename", newPath, syscall.EXDEV)
	}
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
	m.touchParent(from)
	m.touchParent(to)
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
	if m.writeDenied[filepath.Dir(target)] {
		return pathError("remove", path, syscall.EACCES)
	}
	if node.isDir {
		for candidate := range m.nodes {
			if strings.HasPrefix(candidate, strings.TrimSuffix(target, "/")+"/") {
				return pathError("remove", path, syscall.ENOTEMPTY)
			}
		}
	}
	delete(m.nodes, target)
	m.touchParent(target)
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
	m.touchParent(target)
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
	m.touchParent(target)
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
	m.nodes[target] = &memNode{content: &memContent{inode: m.newInode(), data: cloned}, mode: node.mode, modTime: m.now(), uid: -1, gid: -1}
	m.touchParent(target)
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

// touchParent advances the mtime of the directory an entry was added to or removed
// from, which is what a real filesystem does and what the section 10.4 sweep reads to
// decide whether a directory is worth opening. Rewriting a file's contents does not
// reach here: that changes the file, not the listing.
func (m *Mem) touchParent(path string) {
	parent := filepath.Dir(path)
	if node, ok := m.nodes[parent]; ok && node.isDir {
		node.modTime = m.now()
	}
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
