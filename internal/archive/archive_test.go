package archive_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/makearr/MediaNexus/internal/archive"
	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/fsx"
)

func TestDetectFormat(t *testing.T) {
	cases := map[string]archive.Format{
		"Album.zip": archive.FormatZip, "Comic.cbz": archive.FormatZip,
		"Album.rar": archive.FormatRar, "Comic.cbr": archive.FormatRar,
		"Backup.tar": archive.FormatTar, "Backup.tar.gz": archive.FormatTarGz,
		"Backup.tgz": archive.FormatTarGz, "Backup.tar.bz2": archive.FormatTarBz2,
		"Backup.tar.xz": archive.FormatTarXz, "Set.7z": archive.Format7z,
	}
	for name, want := range cases {
		got, ok := archive.DetectFormat(name)
		if !ok || got != want {
			t.Errorf("DetectFormat(%q) = (%q, %v), want (%q, true)", name, got, ok, want)
		}
	}
	if _, ok := archive.DetectFormat("track.flac"); ok {
		t.Error("DetectFormat(track.flac) recognised a non-archive as one")
	}
}

// writeZip builds a real zip file at path containing entries (name -> content).
func writeZip(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer func() { _ = file.Close() }()

	writer := zip.NewWriter(file)
	for name, content := range entries {
		w, err := writer.Create(name)
		if err != nil {
			t.Fatalf("zip create %q: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("zip write %q: %v", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
}

func TestList_ZipReturnsEveryEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "album.zip")
	writeZip(t, path, map[string]string{
		"01.flac": "one", "02.flac": "two-two",
	})

	entries, format, err := archive.List(t.Context(), fsx.NewOS(), path)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if format != archive.FormatZip {
		t.Errorf("format = %q, want zip", format)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %+v, want 2", entries)
	}
	byName := map[string]archive.Entry{}
	for _, e := range entries {
		byName[e.Name] = e
	}
	if byName["01.flac"].SizeBytes != 3 || byName["02.flac"].SizeBytes != 7 {
		t.Errorf("entries = %+v, want the real uncompressed sizes", byName)
	}
	if !byName["01.flac"].HasCRC32 {
		t.Error("want zip entries to carry a CRC32")
	}
}

func TestList_NeverWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "album.zip")
	writeZip(t, path, map[string]string{"01.flac": "one"})

	before, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if _, _, err := archive.List(t.Context(), fsx.NewOS(), path); err != nil {
		t.Fatalf("List: %v", err)
	}
	after, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(before) != len(after) {
		t.Errorf("directory contents changed after List: before=%d after=%d", len(before), len(after))
	}
}

// newExtractFixture is the round-trip fixture: real fsx.OS over a real
// temp directory, matching how Extract is actually used (source and
// destination are always the same real filesystem in production).
func newExtractFixture(t *testing.T) (fsys fsx.FS, destDir string) {
	t.Helper()
	return fsx.NewOS(), filepath.Join(t.TempDir(), "extract-dest")
}

// zipBytes builds a real zip in memory, for the fsx.Mem safety fixture below
// -- writeZip's own real-disk version is for the round-trip and List tests,
// which read through fsx.NewOS() anyway.
func zipBytes(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for name, content := range entries {
		w, err := writer.Create(name)
		if err != nil {
			t.Fatalf("zip create %q: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("zip write %q: %v", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// newSafetyFixture builds source (a zip of entries) and destDir inside one
// fsx.Mem, so a guard that fails to hold cannot reach the real filesystem at
// all -- the traversal test crafts entries designed to escape, and the
// worst a broken guard can do here is write somewhere else inside the same
// fake filesystem.
func newSafetyFixture(t *testing.T, entries map[string]string) (fsys *fsx.Mem, source, destDir string) {
	t.Helper()
	fsys = fsx.NewMem(clock.NewFakeAtEpoch())
	fsys.SetDiskUsage(100<<30, 50<<30)
	if err := fsys.MkdirAll("/downloads", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	source = "/downloads/album.zip"
	writer, err := fsys.Create(source)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := writer.Write(zipBytes(t, entries)); err != nil {
		t.Fatalf("write zip bytes: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return fsys, source, "/work/extract/session-1"
}

func TestExtract_ZipRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "album.zip")
	writeZip(t, path, map[string]string{
		"Artist/Album/01.flac": "one", "Artist/Album/02.flac": "two-two",
	})
	fsys, destDir := newExtractFixture(t)

	if err := archive.Extract(t.Context(), fsys, path, destDir, archive.DefaultLimits()); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	for name, want := range map[string]string{
		"Artist/Album/01.flac": "one", "Artist/Album/02.flac": "two-two",
	} {
		got, err := os.ReadFile(filepath.Join(destDir, name))
		if err != nil {
			t.Fatalf("ReadFile(%q): %v", name, err)
		}
		if string(got) != want {
			t.Errorf("content of %q = %q, want %q", name, got, want)
		}
	}
}

// The mutation-tested guard todo 4.10 asks for by name: a crafted "../"
// entry must never write outside destDir.
func TestExtract_RefusesPathTraversal(t *testing.T) {
	fsys, source, destDir := newSafetyFixture(t, map[string]string{
		"../../../etc/passwd": "pwned",
	})

	err := archive.Extract(t.Context(), fsys, source, destDir, archive.DefaultLimits())
	if err == nil {
		t.Fatal("Extract succeeded for a crafted \"../\" entry, want it refused")
	}
	if _, statErr := fsys.Stat("/etc/passwd"); statErr == nil {
		t.Fatal("the crafted entry escaped destDir and was written to /etc/passwd")
	}
}

// Same guard, an absolute path this time -- the other half of the same
// attack splitEntryPath's own doc comment names.
func TestExtract_RefusesAnAbsolutePathEntry(t *testing.T) {
	fsys, source, destDir := newSafetyFixture(t, map[string]string{
		"/etc/passwd": "pwned",
	})

	if err := archive.Extract(t.Context(), fsys, source, destDir, archive.DefaultLimits()); err != nil {
		// Refusing outright is also acceptable; what matters is it never
		// lands outside destDir. Fall through to the same assertion below.
		_ = err
	}
	if _, statErr := fsys.Stat("/etc/passwd"); statErr == nil {
		t.Fatal("an absolute-path entry escaped destDir and was written to /etc/passwd")
	}
}

func TestExtract_RefusesWhenEntryCountExceedsTheLimit(t *testing.T) {
	entries := map[string]string{}
	for i := range 5 {
		entries[fmt.Sprintf("file-%d.txt", i)] = "x"
	}
	fsys, source, destDir := newSafetyFixture(t, entries)

	limits := archive.DefaultLimits()
	limits.MaxEntryCount = 2
	if err := archive.Extract(t.Context(), fsys, source, destDir, limits); err == nil {
		t.Fatal("Extract succeeded despite exceeding MaxEntryCount")
	}
}

func TestExtract_RefusesWhenTotalBytesExceedTheLimit(t *testing.T) {
	fsys, source, destDir := newSafetyFixture(t, map[string]string{"big.bin": "0123456789"})

	limits := archive.DefaultLimits()
	limits.MaxTotalBytes = 5
	if err := archive.Extract(t.Context(), fsys, source, destDir, limits); err == nil {
		t.Fatal("Extract succeeded despite exceeding MaxTotalBytes")
	}
}

func TestExtract_RefusesWhenFreeSpaceIsBelowTheMargin(t *testing.T) {
	fsys, source, destDir := newSafetyFixture(t, map[string]string{"01.flac": "one"})
	fsys.SetDiskUsage(100<<30, 10) // 10 bytes free

	limits := archive.DefaultLimits()
	limits.MinFreeBytes = 1 << 20
	if err := archive.Extract(t.Context(), fsys, source, destDir, limits); err == nil {
		t.Fatal("Extract succeeded despite free space below the required margin")
	}
}

func TestExtract_RefusesAnEncryptedZipEntry(t *testing.T) {
	// archive/zip's writer cannot itself produce an encrypted entry, so the
	// general-purpose flag bit is set by hand on a real, otherwise-valid zip.
	path := filepath.Join(t.TempDir(), "encrypted.zip")
	writeZip(t, path, map[string]string{"secret.txt": "shh"})
	setZipEncryptedFlag(t, path)

	fsys, destDir := newExtractFixture(t)
	err := archive.Extract(t.Context(), fsys, path, destDir, archive.DefaultLimits())
	if !errors.Is(err, archive.ErrEncrypted) {
		t.Errorf("err = %v, want ErrEncrypted", err)
	}
}

// setZipEncryptedFlag flips the general-purpose bit-0 flag in a real zip
// file's local and central-directory headers, both of which store it at a
// fixed byte offset from the start of each header's own signature.
func setZipEncryptedFlag(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	const (
		localSig                 = "PK\x03\x04"
		centralSig               = "PK\x01\x02"
		flagOffsetFromLocalSig   = 6
		flagOffsetFromCentralSig = 8
	)
	flip := func(sig string, flagOffset int) {
		for i := 0; i+len(sig) <= len(data); i++ {
			if string(data[i:i+len(sig)]) == sig {
				data[i+flagOffset] |= 0x1
			}
		}
	}
	flip(localSig, flagOffsetFromLocalSig)
	flip(centralSig, flagOffsetFromCentralSig)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// writeTarGz builds a real .tar.gz fixture -- proof the format's own
// decompression wrapper is wired correctly, not just plain tar.
func writeTarGz(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer func() { _ = file.Close() }()

	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)
	for name, content := range entries {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Size: int64(len(content)), Mode: 0o644, Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatalf("tar header %q: %v", name, err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("tar write %q: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
}

func TestList_TarGzReturnsEveryEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "album.tar.gz")
	writeTarGz(t, path, map[string]string{"01.flac": "one"})

	entries, format, err := archive.List(t.Context(), fsx.NewOS(), path)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if format != archive.FormatTarGz {
		t.Errorf("format = %q, want tar.gz", format)
	}
	if len(entries) != 1 || entries[0].Name != "01.flac" || entries[0].SizeBytes != 3 {
		t.Errorf("entries = %+v, want one 01.flac of size 3", entries)
	}
}

func TestExtract_TarGzRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "album.tar.gz")
	writeTarGz(t, path, map[string]string{"Artist/Album/01.flac": "one"})
	fsys, destDir := newExtractFixture(t)

	if err := archive.Extract(t.Context(), fsys, path, destDir, archive.DefaultLimits()); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if _, err := fsys.Stat(filepath.Join(destDir, "Artist/Album/01.flac")); err != nil {
		t.Errorf("extracted file not found: %v", err)
	}
}

func TestDetectMultipart_PartNumberedRarSet(t *testing.T) {
	names := []string{
		"Album.part01.rar", "Album.part02.rar", "Album.part03.rar", "cover.jpg",
	}
	sets := archive.DetectMultipart(names)
	set, ok := sets["Album"]
	if !ok {
		t.Fatalf("sets = %+v, want a set keyed \"Album\"", sets)
	}
	if set.Primary != "Album.part01.rar" {
		t.Errorf("Primary = %q, want the first volume", set.Primary)
	}
	if len(set.MissingVolumes()) != 0 {
		t.Errorf("MissingVolumes = %v, want none: all three parts present", set.MissingVolumes())
	}
}

func TestDetectMultipart_ReportsWhichVolumeIsMissing(t *testing.T) {
	names := []string{"Album.part01.rar", "Album.part03.rar"}
	set := archive.DetectMultipart(names)["Album"]
	missing := set.MissingVolumes()
	if len(missing) != 1 || missing[0] != 2 {
		t.Errorf("MissingVolumes = %v, want [2] -- part 2 of 3 is the gap, not \"corrupt\"", missing)
	}
}

func TestDetectMultipart_RNumberedSet(t *testing.T) {
	names := []string{"Album.rar", "Album.r00", "Album.r01"}
	set := archive.DetectMultipart(names)["Album"]
	if set == nil {
		t.Fatal("want a detected set for the .rar/.r00/.r01 scheme")
	}
	if set.Primary != "Album.rar" {
		t.Errorf("Primary = %q, want Album.rar (volume 1)", set.Primary)
	}
	if len(set.MissingVolumes()) != 0 {
		t.Errorf("MissingVolumes = %v, want none", set.MissingVolumes())
	}
}

func TestDetectMultipart_SevenZipNumberedSet(t *testing.T) {
	names := []string{"Album.7z.001", "Album.7z.002"}
	set := archive.DetectMultipart(names)["Album.7z"]
	if set == nil {
		t.Fatal("want a detected set for the .7z.NNN scheme")
	}
	if len(set.MissingVolumes()) != 0 {
		t.Errorf("MissingVolumes = %v, want none", set.MissingVolumes())
	}
}

// A standalone .rar with no .rNN siblings must not be misdetected as a
// one-volume "set" -- every real single-volume rar would otherwise show up
// as a multipart set of size one.
func TestDetectMultipart_StandaloneRarIsNotASet(t *testing.T) {
	sets := archive.DetectMultipart([]string{"Album.rar", "cover.jpg"})
	if _, ok := sets["Album"]; ok {
		t.Error("a standalone .rar with no .rNN sibling was detected as a multipart set")
	}
}
