package archive

import (
	"archive/tar"
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"path"

	"github.com/bodgit/sevenzip"
	"github.com/nwaples/rardecode/v2"

	"github.com/makearr/MediaNexus/internal/fsx"
)

// Extract expands source into destDir, subject to limits. destDir is always
// a workspace the caller owns (ADR-49: "Extraction happens in a workspace,
// never in place") -- this package has no opinion on where that workspace
// is, only that every write stays inside it, checked by fsx.SafeJoin
// (Invariant 76, the ADR-14 choke point applied to untrusted archive entry
// names). Reading the source archive goes through fsx too, except rar --
// see List's own doc comment for why that one format is the exception.
func Extract(ctx context.Context, fsys fsx.FS, source, destDir string, limits Limits) error {
	format, ok := DetectFormat(source)
	if !ok {
		return fmt.Errorf("archive: %s: unrecognised format", source)
	}
	info, err := fsys.Stat(source)
	if err != nil {
		return fmt.Errorf("archive: stat %s: %w", source, err)
	}
	if err := checkFreeSpace(fsys, destDir, limits); err != nil {
		return err
	}

	b := &budget{limits: limits, sourceBytes: info.Size}
	if format == FormatRar {
		return extractRar(ctx, fsys, source, destDir, b)
	}

	file, err := fsys.Open(source)
	if err != nil {
		return fmt.Errorf("archive: open %s: %w", source, err)
	}
	defer func() { _ = file.Close() }()

	switch format {
	case FormatZip:
		return extractZip(ctx, fsys, file, info.Size, destDir, b)
	case FormatTar, FormatTarGz, FormatTarBz2, FormatTarXz:
		return extractTar(ctx, fsys, file, format, destDir, b)
	case Format7z:
		return extract7z(ctx, fsys, file, info.Size, destDir, b)
	default:
		return fmt.Errorf("archive: %s: %s extraction not implemented", source, format)
	}
}

// checkFreeSpace is the free-space margin check "before the first byte is
// written" (ADR-49) -- destDir's parent must already exist for DiskUsage to
// resolve a real filesystem, so this runs before any directory is created
// under it either.
func checkFreeSpace(fsys fsx.FS, destDir string, limits Limits) error {
	probeDir := destDir
	for {
		_, free, err := fsys.DiskUsage(probeDir)
		if err == nil {
			if free < limits.MinFreeBytes {
				return fmt.Errorf("archive: %s: only %d bytes free, want at least %d",
					probeDir, free, limits.MinFreeBytes)
			}
			return nil
		}
		parent := path.Dir(probeDir)
		if parent == probeDir {
			return fmt.Errorf("archive: could not determine free space for %s: %w", destDir, err)
		}
		probeDir = parent
	}
}

// budget enforces the three numeric limits ADR-49 names as one shared
// bookkeeper across every format's own extraction loop: entry count, total
// uncompressed bytes, and expansion ratio (measured against the source
// archive's own on-disk size, since not every format exposes a reliable
// per-entry compressed size). Checked as bytes are actually written, not
// only at entry boundaries, so a single oversized entry cannot blow through
// the limit before the next check would have caught it.
type budget struct {
	limits            Limits
	sourceBytes       int64
	uncompressedTotal int64
	entries           int
}

func (b *budget) admitEntry() error {
	b.entries++
	if b.entries > b.limits.MaxEntryCount {
		return fmt.Errorf("archive: entry count exceeds the limit of %d", b.limits.MaxEntryCount)
	}
	return nil
}

func (b *budget) addBytes(n int64) error {
	b.uncompressedTotal += n
	if b.uncompressedTotal > b.limits.MaxTotalBytes {
		return fmt.Errorf("archive: extracted size exceeds the limit of %d bytes", b.limits.MaxTotalBytes)
	}
	if b.sourceBytes > 0 {
		ratio := float64(b.uncompressedTotal) / float64(b.sourceBytes)
		if ratio > b.limits.MaxExpansionRatio {
			return fmt.Errorf("archive: expansion ratio %.1fx exceeds the limit of %.1fx",
				ratio, b.limits.MaxExpansionRatio)
		}
	}
	return nil
}

// limitedWriter counts bytes through to dest via budget, so the ratio and
// total-bytes checks run against real progress mid-stream, not only once an
// entry has finished decompressing.
type limitedWriter struct {
	dest   io.Writer
	budget *budget
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if err := w.budget.addBytes(int64(len(p))); err != nil {
		return 0, err
	}
	return w.dest.Write(p)
}

// extractEntry resolves name safely beneath destDir (Invariant 76) and
// copies from src into it, subject to b. A directory entry only needs its
// own directory created; src is nil for one.
func extractEntry(fsys fsx.FS, destDir, name string, isDir bool, src io.Reader, b *budget) error {
	components := splitEntryPath(name)
	if len(components) == 0 {
		return fmt.Errorf("archive: entry %q has no usable path", name)
	}
	resolved, err := fsx.SafeJoin(fsys, destDir, components...)
	if err != nil {
		return fmt.Errorf("archive: entry %q: %w", name, err)
	}

	if isDir {
		return fsys.MkdirAll(resolved, 0o755)
	}
	if err := fsys.MkdirAll(path.Dir(resolved), 0o755); err != nil {
		return fmt.Errorf("archive: create directory for %q: %w", name, err)
	}
	writer, err := fsys.Create(resolved)
	if err != nil {
		return fmt.Errorf("archive: create %q: %w", name, err)
	}
	defer func() { _ = writer.Close() }()

	if _, err := io.Copy(&limitedWriter{dest: writer, budget: b}, src); err != nil {
		return fmt.Errorf("archive: extract %q: %w", name, err)
	}
	return nil
}

// splitEntryPath turns an archive entry's own name (always "/"-separated,
// whatever format it came from) into path components fsx.SafeJoin can
// sanitise and check one at a time. A leading "/" or drive letter is
// stripped rather than trusted -- an absolute path inside an archive entry
// is exactly the other half of the traversal attack SafeJoin's own ".."
// rejection guards against.
func splitEntryPath(name string) []string {
	var components []string
	for _, part := range pathSplit(name) {
		if part == "" || part == "." {
			continue
		}
		components = append(components, part)
	}
	return components
}

func pathSplit(name string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(name); i++ {
		if name[i] == '/' || name[i] == '\\' {
			parts = append(parts, name[start:i])
			start = i + 1
		}
	}
	parts = append(parts, name[start:])
	return parts
}

func extractZip(ctx context.Context, fsys fsx.FS, r io.ReaderAt, size int64, destDir string, b *budget) error {
	reader, err := zip.NewReader(r, size)
	if err != nil && !errors.Is(err, zip.ErrInsecurePath) {
		return fmt.Errorf("archive: open zip: %w", err)
	}

	for _, f := range reader.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := b.admitEntry(); err != nil {
			return err
		}
		if f.Flags&zipEncryptedFlag != 0 {
			return fmt.Errorf("archive: entry %q: %w", f.Name, ErrEncrypted)
		}
		isDir := f.FileInfo().IsDir()
		if isDir {
			if err := extractEntry(fsys, destDir, f.Name, true, nil, b); err != nil {
				return err
			}
			continue
		}
		src, err := f.Open()
		if err != nil {
			return fmt.Errorf("archive: open entry %q: %w", f.Name, err)
		}
		err = extractEntry(fsys, destDir, f.Name, false, src, b)
		_ = src.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func extractTar(ctx context.Context, fsys fsx.FS, r io.Reader, format Format, destDir string, b *budget) error {
	reader, err := tarReaderFor(r, format)
	if err != nil {
		return fmt.Errorf("archive: %s: %w", format, err)
	}

	tr := tar.NewReader(reader)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("archive: read tar: %w", err)
		}
		if err := b.admitEntry(); err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := extractEntry(fsys, destDir, header.Name, true, nil, b); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := extractEntry(fsys, destDir, header.Name, false, tr, b); err != nil {
				return err
			}
		default:
			// Symlinks, hardlinks, device files: never followed or created
			// (the same "never a symlink" rule ADR-11 already applies to
			// import), silently skipped rather than refusing the whole
			// archive over one entry a music or book release never has a
			// legitimate reason to contain.
		}
	}
}

// extractRar is the one format read by real path rather than through fsx --
// see List's own doc comment for why.
func extractRar(ctx context.Context, fsys fsx.FS, source, destDir string, b *budget) error {
	reader, err := rardecode.OpenReader(source)
	if err != nil {
		return fmt.Errorf("archive: open rar %s: %w", source, err)
	}
	defer func() { _ = reader.Close() }()

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("archive: read rar %s: %w", source, err)
		}
		if err := b.admitEntry(); err != nil {
			return err
		}
		if header.Encrypted {
			return fmt.Errorf("archive: entry %q: %w", header.Name, ErrEncrypted)
		}
		if err := extractEntry(fsys, destDir, header.Name, header.IsDir, reader, b); err != nil {
			return err
		}
	}
}

func extract7z(ctx context.Context, fsys fsx.FS, r io.ReaderAt, size int64, destDir string, b *budget) error {
	reader, err := sevenzip.NewReader(r, size)
	if err != nil {
		return fmt.Errorf("archive: open 7z: %w", err)
	}

	for _, f := range reader.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := b.admitEntry(); err != nil {
			return err
		}
		isDir := f.FileInfo().IsDir()
		if isDir {
			if err := extractEntry(fsys, destDir, f.Name, true, nil, b); err != nil {
				return err
			}
			continue
		}
		src, err := f.Open()
		if err != nil {
			// bodgit/sevenzip answers a password-shaped error for an
			// encrypted entry rather than a typed sentinel this package
			// could match on directly -- recorded here rather than
			// misreported as ErrEncrypted, since todo 4.10 explicitly wants
			// an unsupported case named with its own format and error
			// rather than guessed at.
			return fmt.Errorf("archive: open 7z entry %q: %w", f.Name, err)
		}
		err = extractEntry(fsys, destDir, f.Name, false, src, b)
		_ = src.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
