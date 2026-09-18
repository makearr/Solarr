package archive

import (
	"archive/tar"
	"archive/zip"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/bodgit/sevenzip"
	"github.com/nwaples/rardecode/v2"
	"github.com/ulikunitz/xz"

	"github.com/makearr/Solarr/internal/platform/fsx"
)

// List reads path's entries without writing anything (ADR-49: "no writes,
// ever") -- names, sizes and (where the format carries one) CRCs, enough to
// identify what an archive holds and record it as PACKED presence against a
// desired item without touching a byte. Every read goes through fsx (the
// ADR-14 choke point) except rar, whose multi-volume support needs to open
// sibling files next to path by name -- real filesystem awareness a Reader
// over one already-open file cannot provide -- so listRar is given the
// path directly, the one exception, same reasoning as internal/platform/mediatool's
// own real-path subprocess reads.
func List(ctx context.Context, fsys fsx.FS, path string) ([]Entry, Format, error) {
	format, ok := DetectFormat(path)
	if !ok {
		return nil, "", fmt.Errorf("archive: %s: unrecognised format", path)
	}
	if err := ctx.Err(); err != nil {
		return nil, format, err
	}

	if format == FormatRar {
		entries, err := listRar(path)
		return entries, format, err
	}

	info, err := fsys.Stat(path)
	if err != nil {
		return nil, format, fmt.Errorf("archive: stat %s: %w", path, err)
	}
	file, err := fsys.Open(path)
	if err != nil {
		return nil, format, fmt.Errorf("archive: open %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()

	switch format {
	case FormatZip:
		entries, err := listZip(file, info.Size)
		return entries, format, err
	case FormatTar, FormatTarGz, FormatTarBz2, FormatTarXz:
		entries, err := listTar(ctx, file, format)
		return entries, format, err
	case Format7z:
		entries, err := list7z(file, info.Size)
		return entries, format, err
	default:
		return nil, format, fmt.Errorf("archive: %s: %s listing not implemented", path, format)
	}
}

// zipEncryptedFlag is bit 0 of a ZIP local file header's general-purpose
// flag field -- the PKWARE spec's own encryption bit. archive/zip exposes
// the raw Flags field but no IsEncrypted method in this Go version.
const zipEncryptedFlag = 0x1

func listZip(r io.ReaderAt, size int64) ([]Entry, error) {
	reader, err := zip.NewReader(r, size)
	if err != nil && !errors.Is(err, zip.ErrInsecurePath) {
		return nil, fmt.Errorf("archive: open zip: %w", err)
	}

	entries := make([]Entry, 0, len(reader.File))
	for _, f := range reader.File {
		entries = append(entries, Entry{
			Name: f.Name, SizeBytes: int64(f.UncompressedSize64),
			CRC32: f.CRC32, HasCRC32: true, IsDir: f.FileInfo().IsDir(),
			Encrypted: f.Flags&zipEncryptedFlag != 0,
		})
	}
	return entries, nil
}

func listTar(ctx context.Context, r io.Reader, format Format) ([]Entry, error) {
	reader, err := tarReaderFor(r, format)
	if err != nil {
		return nil, fmt.Errorf("archive: %s: %w", format, err)
	}

	var entries []Entry
	tr := tar.NewReader(reader)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("archive: read tar: %w", err)
		}
		entries = append(entries, Entry{
			Name: header.Name, SizeBytes: header.Size, IsDir: header.Typeflag == tar.TypeDir,
		})
	}
	return entries, nil
}

// tarReaderFor wraps r in whatever decompressor format needs, or returns it
// unwrapped for a plain, uncompressed .tar.
func tarReaderFor(r io.Reader, format Format) (io.Reader, error) {
	switch format {
	case FormatTarGz:
		return gzip.NewReader(r)
	case FormatTarBz2:
		return bzip2.NewReader(r), nil
	case FormatTarXz:
		return xz.NewReader(r)
	default:
		return r, nil
	}
}

// listRar is the one format read by real path rather than through fsx --
// see List's own doc comment for why.
func listRar(path string) ([]Entry, error) {
	files, err := rardecode.List(path)
	if err != nil {
		return nil, fmt.Errorf("archive: open rar %s: %w", path, err)
	}
	entries := make([]Entry, 0, len(files))
	for _, f := range files {
		entries = append(entries, Entry{
			Name: f.Name, SizeBytes: f.UnPackedSize, IsDir: f.IsDir, Encrypted: f.Encrypted,
		})
	}
	return entries, nil
}

func list7z(r io.ReaderAt, size int64) ([]Entry, error) {
	reader, err := sevenzip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("archive: open 7z: %w", err)
	}

	entries := make([]Entry, 0, len(reader.File))
	for _, f := range reader.File {
		entries = append(entries, Entry{
			Name: f.Name, SizeBytes: int64(f.UncompressedSize),
			CRC32: f.CRC32, HasCRC32: true, IsDir: f.FileInfo().IsDir(),
		})
	}
	return entries, nil
}
