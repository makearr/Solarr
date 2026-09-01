package fsx_test

import (
	"errors"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/fsx"
)

// Both implementations run the same suite. A fake that behaves differently from
// the real filesystem makes every test above it a lie, so the two are held to
// one specification rather than tested separately (ADR-17).
func TestConformance(t *testing.T) {
	implementations := []struct {
		name string
		open func(t *testing.T) (fsx.FS, string)
	}{
		{"Mem", newMemUnderTest},
		{"OS", newOSUnderTest},
	}

	suite := []struct {
		name string
		run  func(t *testing.T, fsys fsx.FS, root string)
	}{
		{"CreateThenRead", conformCreateThenRead},
		{"StatReportsSizeAndKind", conformStat},
		{"MkdirAllIsIdempotent", conformMkdirAll},
		{"ReadDirListsDirectChildrenSorted", conformReadDir},
		{"RenameMovesAFile", conformRename},
		{"RemoveRefusesANonEmptyDirectory", conformRemoveNonEmpty},
		{"MissingFileIsErrNotExist", conformMissingFile},
		{"HardlinkSharesContent", conformHardlinkSharesContent},
		{"LinkOntoAnExistingPathFails", conformLinkExists},
		{"StatFollowsASymlinkAndLstatDoesNot", conformSymlinkStat},
		{"ResolveKeepsANonExistentTail", conformResolveTail},
		{"ResolveFollowsASymlinkedDirectory", conformResolveSymlinkedDir},
		{"OpenSupportsSeekAndReadAt", conformSeekAndReadAt},
		{"SafeJoinRejectsTraversal", conformSafeJoinTraversal},
		{"SafeJoinRejectsASymlinkEscape", conformSafeJoinSymlinkEscape},
		{"SafeJoinAcceptsAContainedPath", conformSafeJoinContained},
		{"WriteAtomicLeavesNoTemporaryOnFailure", conformWriteAtomicFailure},
		{"CreateSecretIsNotReadableByOthers", conformCreateSecretMode},
		{"WriteAtomicSecretLandsRestricted", conformWriteAtomicSecretMode},
	}

	for _, implementation := range implementations {
		for _, testCase := range suite {
			t.Run(implementation.name+"/"+testCase.name, func(t *testing.T) {
				fsys, root := implementation.open(t)
				testCase.run(t, fsys, root)
			})
		}
	}
}

func newMemUnderTest(t *testing.T) (fsx.FS, string) {
	t.Helper()
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	if err := fsys.MkdirAll("/library", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	return fsys, "/library"
}

func newOSUnderTest(t *testing.T) (fsx.FS, string) {
	t.Helper()
	fsys := fsx.NewOS()
	// The temp directory may itself sit behind a symlink, which would make every
	// containment assertion below fail for the wrong reason.
	root, err := fsys.Resolve(t.TempDir())
	if err != nil {
		t.Fatalf("Resolve(TempDir): %v", err)
	}
	return fsys, root
}

func writeFile(t *testing.T, fsys fsx.FS, path, content string) {
	t.Helper()
	file, err := fsys.Create(path)
	if err != nil {
		t.Fatalf("Create(%q): %v", path, err)
	}
	if _, err := io.WriteString(file, content); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close %q: %v", path, err)
	}
}

func readFile(t *testing.T, fsys fsx.FS, path string) string {
	t.Helper()
	file, err := fsys.Open(path)
	if err != nil {
		t.Fatalf("Open(%q): %v", path, err)
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	return string(content)
}

func conformCreateThenRead(t *testing.T, fsys fsx.FS, root string) {
	path := filepath.Join(root, "track.flac")
	writeFile(t, fsys, path, "audio")
	if got := readFile(t, fsys, path); got != "audio" {
		t.Errorf("read back %q, want %q", got, "audio")
	}
}

func conformStat(t *testing.T, fsys fsx.FS, root string) {
	path := filepath.Join(root, "track.flac")
	writeFile(t, fsys, path, "audio")

	info, err := fsys.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Name != "track.flac" {
		t.Errorf("Name = %q, want %q", info.Name, "track.flac")
	}
	if info.Size != int64(len("audio")) {
		t.Errorf("Size = %d, want %d", info.Size, len("audio"))
	}
	if info.IsDir {
		t.Error("IsDir = true for a file")
	}

	dirInfo, err := fsys.Stat(root)
	if err != nil {
		t.Fatalf("Stat(root): %v", err)
	}
	if !dirInfo.IsDir {
		t.Error("IsDir = false for a directory")
	}
}

func conformMkdirAll(t *testing.T, fsys fsx.FS, root string) {
	nested := filepath.Join(root, "Artist", "Album", "Disc 1")
	for attempt := range 2 {
		if err := fsys.MkdirAll(nested, 0o755); err != nil {
			t.Fatalf("MkdirAll attempt %d: %v", attempt, err)
		}
	}
	info, err := fsys.Stat(nested)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !info.IsDir {
		t.Error("MkdirAll did not create a directory")
	}
}

func conformReadDir(t *testing.T, fsys fsx.FS, root string) {
	if err := fsys.MkdirAll(filepath.Join(root, "b-dir", "grandchild"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeFile(t, fsys, filepath.Join(root, "a-file"), "x")
	writeFile(t, fsys, filepath.Join(root, "c-file"), "x")

	entries, err := fsys.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	want := []fsx.DirEntry{
		{Name: "a-file", IsDir: false},
		{Name: "b-dir", IsDir: true},
		{Name: "c-file", IsDir: false},
	}
	if len(entries) != len(want) {
		t.Fatalf("ReadDir = %v, want %v", entries, want)
	}
	for i := range want {
		if entries[i] != want[i] {
			t.Errorf("entry %d = %v, want %v", i, entries[i], want[i])
		}
	}
}

func conformRename(t *testing.T, fsys fsx.FS, root string) {
	from, to := filepath.Join(root, "old"), filepath.Join(root, "new")
	writeFile(t, fsys, from, "content")

	if err := fsys.Rename(from, to); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if got := readFile(t, fsys, to); got != "content" {
		t.Errorf("renamed file = %q, want %q", got, "content")
	}
	if _, err := fsys.Stat(from); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(old) = %v, want ErrNotExist", err)
	}
}

func conformRemoveNonEmpty(t *testing.T, fsys fsx.FS, root string) {
	directory := filepath.Join(root, "album")
	if err := fsys.MkdirAll(directory, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeFile(t, fsys, filepath.Join(directory, "track.flac"), "audio")

	if err := fsys.Remove(directory); err == nil {
		t.Fatal("Remove of a non-empty directory succeeded; it must not")
	}
}

func conformMissingFile(t *testing.T, fsys fsx.FS, root string) {
	if _, err := fsys.Stat(filepath.Join(root, "absent")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(absent) = %v, want ErrNotExist", err)
	}
	if _, err := fsys.Open(filepath.Join(root, "absent")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Open(absent) = %v, want ErrNotExist", err)
	}
}

func conformHardlinkSharesContent(t *testing.T, fsys fsx.FS, root string) {
	original, linked := filepath.Join(root, "a.flac"), filepath.Join(root, "b.flac")
	writeFile(t, fsys, original, "first")

	if err := fsys.Link(original, linked); err != nil {
		t.Fatalf("Link: %v", err)
	}
	if got := readFile(t, fsys, linked); got != "first" {
		t.Errorf("link reads %q, want %q", got, "first")
	}

	// One inode, two directory entries: a rewrite through either is seen by both.
	writeFile(t, fsys, original, "second")
	if got := readFile(t, fsys, linked); got != "second" {
		t.Errorf("after rewriting the original, the link reads %q, want %q", got, "second")
	}
}

func conformLinkExists(t *testing.T, fsys fsx.FS, root string) {
	original, occupied := filepath.Join(root, "a.flac"), filepath.Join(root, "b.flac")
	writeFile(t, fsys, original, "first")
	writeFile(t, fsys, occupied, "already here")

	if err := fsys.Link(original, occupied); !errors.Is(err, fs.ErrExist) {
		t.Errorf("Link onto an existing path = %v, want ErrExist", err)
	}
	if got := readFile(t, fsys, occupied); got != "already here" {
		t.Errorf("the existing file was overwritten: %q", got)
	}
}

func conformSymlinkStat(t *testing.T, fsys fsx.FS, root string) {
	target, link := filepath.Join(root, "target"), filepath.Join(root, "link")
	writeFile(t, fsys, target, "content")
	if err := fsys.Symlink(target, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	statInfo, err := fsys.Stat(link)
	if err != nil {
		t.Fatalf("Stat(link): %v", err)
	}
	if statInfo.Mode&fs.ModeSymlink != 0 {
		t.Error("Stat did not follow the symlink")
	}
	if statInfo.Size != int64(len("content")) {
		t.Errorf("Stat(link).Size = %d, want %d", statInfo.Size, len("content"))
	}

	lstatInfo, err := fsys.Lstat(link)
	if err != nil {
		t.Fatalf("Lstat(link): %v", err)
	}
	if lstatInfo.Mode&fs.ModeSymlink == 0 {
		t.Error("Lstat followed the symlink; it must not")
	}
}

func conformResolveTail(t *testing.T, fsys fsx.FS, root string) {
	wanted := filepath.Join(root, "not", "created", "yet.flac")
	resolved, err := fsys.Resolve(wanted)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved != wanted {
		t.Errorf("Resolve = %q, want %q — a path must be checkable before it exists", resolved, wanted)
	}
}

func conformResolveSymlinkedDir(t *testing.T, fsys fsx.FS, root string) {
	actual, link := filepath.Join(root, "actual"), filepath.Join(root, "link")
	if err := fsys.MkdirAll(actual, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := fsys.Symlink(actual, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	resolved, err := fsys.Resolve(filepath.Join(link, "track.flac"))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if want := filepath.Join(actual, "track.flac"); resolved != want {
		t.Errorf("Resolve = %q, want %q", resolved, want)
	}
}

func conformSeekAndReadAt(t *testing.T, fsys fsx.FS, root string) {
	path := filepath.Join(root, "track.flac")
	writeFile(t, fsys, path, "0123456789")

	file, err := fsys.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = file.Close() }()

	// oshash reads the tail of a file without reading the middle (ADR-35).
	if _, err := file.Seek(-4, io.SeekEnd); err != nil {
		t.Fatalf("Seek: %v", err)
	}
	tail, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("read tail: %v", err)
	}
	if string(tail) != "6789" {
		t.Errorf("tail = %q, want %q", tail, "6789")
	}

	middle := make([]byte, 3)
	if _, err := file.ReadAt(middle, 4); err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	if string(middle) != "456" {
		t.Errorf("ReadAt = %q, want %q", middle, "456")
	}
}

func conformSafeJoinTraversal(t *testing.T, fsys fsx.FS, root string) {
	for _, component := range []string{"..", "../..", "../../etc", "/etc/passwd"} {
		joined, err := fsx.SafeJoin(fsys, root, component)
		if err == nil {
			if !fsx.Within(root, joined) {
				t.Errorf("SafeJoin(%q) = %q, which escapes %q", component, joined, root)
			}
			continue
		}
		if !errors.Is(err, fsx.ErrEmptyComponent) && !errors.Is(err, fsx.ErrEscapesRoot) {
			t.Errorf("SafeJoin(%q) = %v, want ErrEmptyComponent or ErrEscapesRoot", component, err)
		}
	}
}

func conformSafeJoinSymlinkEscape(t *testing.T, fsys fsx.FS, root string) {
	// Sanitising the component text cannot see this: the escape is a symlink
	// already on disk. Only resolving before the containment check catches it.
	outside := filepath.Join(filepath.Dir(root), "outside")
	if err := fsys.MkdirAll(outside, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := fsys.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	joined, err := fsx.SafeJoin(fsys, root, "escape", "stolen.flac")
	if !errors.Is(err, fsx.ErrEscapesRoot) {
		t.Fatalf("SafeJoin through a symlink out of the root = (%q, %v), want ErrEscapesRoot", joined, err)
	}
}

func conformSafeJoinContained(t *testing.T, fsys fsx.FS, root string) {
	joined, err := fsx.SafeJoin(fsys, root, "XT/QK", "Kata in Mifed", "01 Metup Nagug.flac")
	if err != nil {
		t.Fatalf("SafeJoin: %v", err)
	}
	if !fsx.Within(root, joined) {
		t.Errorf("SafeJoin = %q, which is not inside %q", joined, root)
	}
	if !strings.Contains(joined, "XT_QK") {
		t.Errorf("SafeJoin = %q, want the separator in %q replaced rather than dropped", joined, "XT/QK")
	}
}

func conformWriteAtomicFailure(t *testing.T, fsys fsx.FS, root string) {
	destination := filepath.Join(root, "nfo.xml")
	wantErr := errors.New("writer exploded")

	err := fsx.WriteAtomic(fsys, destination, func(io.Writer) error { return wantErr })
	if !errors.Is(err, wantErr) {
		t.Fatalf("WriteAtomic = %v, want %v", err, wantErr)
	}
	if _, err := fsys.Stat(destination); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a failed write created the destination: %v", err)
	}

	entries, err := fsys.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		if fsx.IsTemporary(entry.Name) {
			t.Errorf("a failed write left the temporary %q behind", entry.Name)
		}
	}
}

// The encryption key lives in a file this package creates (ADR-14). A mode that
// lets any account on the host read it makes the encryption of settings.db
// decorative, so the mode is asserted rather than assumed.
func conformCreateSecretMode(t *testing.T, fsys fsx.FS, root string) {
	path := filepath.Join(root, "config.yml")
	file, err := fsys.CreateSecret(path)
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}
	if _, err := io.WriteString(file, "encryption_key: base64:xxx"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	info, err := fsys.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if permissions := info.Mode.Perm(); permissions != 0o600 {
		t.Errorf("CreateSecret made mode %04o, want 0600", permissions)
	}
	if info.Mode.Perm()&0o077 != 0 {
		t.Errorf("mode %04o is readable beyond the owner", info.Mode.Perm())
	}
}

// The temporary is what actually holds the secret before the rename, so the
// restriction has to be on the temporary. Asserting only the destination would
// pass even if the key spent its whole write world-readable.
func conformWriteAtomicSecretMode(t *testing.T, fsys fsx.FS, root string) {
	path := filepath.Join(root, "config.yml")

	var temporaryMode fs.FileMode
	err := fsx.WriteAtomicSecret(fsys, path, func(w io.Writer) error {
		entries, readErr := fsys.ReadDir(root)
		if readErr != nil {
			return readErr
		}
		for _, entry := range entries {
			if fsx.IsTemporary(entry.Name) {
				info, statErr := fsys.Stat(filepath.Join(root, entry.Name))
				if statErr != nil {
					return statErr
				}
				temporaryMode = info.Mode.Perm()
			}
		}
		_, writeErr := io.WriteString(w, "encryption_key: base64:xxx")
		return writeErr
	})
	if err != nil {
		t.Fatalf("WriteAtomicSecret: %v", err)
	}

	if temporaryMode == 0 {
		t.Fatal("no temporary was observed mid-write; the test proved nothing")
	}
	if temporaryMode != 0o600 {
		t.Errorf("the temporary holding the key had mode %04o, want 0600", temporaryMode)
	}

	info, err := fsys.Stat(path)
	if err != nil {
		t.Fatalf("stat destination: %v", err)
	}
	if permissions := info.Mode.Perm(); permissions != 0o600 {
		t.Errorf("destination mode %04o, want 0600", permissions)
	}
}
