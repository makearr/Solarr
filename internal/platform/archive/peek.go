package archive

import (
	"archive/tar"
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/bodgit/sevenzip"
	"github.com/nwaples/rardecode/v2"

	"github.com/makearr/Solarr/internal/platform/fsx"
)

// Peeked is one file entry of an archive with the first bytes the caller asked for; Head is nil when none were asked for.
type Peeked struct {
	Entry Entry
	Head  []byte
}

// Peek lists every file entry of path in archive order and reads the first want(entry) bytes of those the caller wants, never writing to disk.
// Reading stops after maxEntries entries; the Entry list is then partial and the second result true.
func Peek(ctx context.Context, fsys fsx.FS, path string, want func(Entry) int64, maxEntries int) ([]Peeked, bool, error) {
	format, ok := detectComicContainer(path)
	if !ok {
		return nil, false, fmt.Errorf("archive: %s: unrecognised format", path)
	}
	if format == FormatRar {
		return peekRar(ctx, path, want, maxEntries)
	}
	info, err := fsys.Stat(path)
	if err != nil {
		return nil, false, fmt.Errorf("archive: stat %s: %w", path, err)
	}
	file, err := fsys.Open(path)
	if err != nil {
		return nil, false, fmt.Errorf("archive: open %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()
	switch format {
	case FormatZip:
		return peekZip(ctx, file, info.Size, want, maxEntries)
	case Format7z:
		return peek7z(ctx, file, info.Size, want, maxEntries)
	default:
		return peekTar(ctx, file, format, want, maxEntries)
	}
}

// detectComicContainer is DetectFormat plus the comic and ebook names for the same containers, which unpacking a download must not treat as archives.
func detectComicContainer(path string) (Format, bool) {
	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, ".cbt"):
		return FormatTar, true
	case strings.HasSuffix(lower, ".cb7"):
		return Format7z, true
	case strings.HasSuffix(lower, ".epub"):
		return FormatZip, true
	default:
		return DetectFormat(path)
	}
}

func headOf(r io.Reader, n int64) ([]byte, error) {
	buf, err := io.ReadAll(io.LimitReader(r, n))
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return buf, err
	}
	return buf, nil
}

func peekZip(ctx context.Context, r io.ReaderAt, size int64, want func(Entry) int64, maxEntries int) ([]Peeked, bool, error) {
	reader, err := zip.NewReader(r, size)
	if err != nil && !errors.Is(err, zip.ErrInsecurePath) {
		return nil, false, fmt.Errorf("archive: open zip: %w", err)
	}
	var out []Peeked
	for _, f := range reader.File {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		if f.FileInfo().IsDir() {
			continue
		}
		if len(out) >= maxEntries {
			return out, true, nil
		}
		entry := Entry{Name: f.Name, SizeBytes: int64(f.UncompressedSize64), CRC32: f.CRC32, HasCRC32: true, Encrypted: f.Flags&zipEncryptedFlag != 0}
		p := Peeked{Entry: entry}
		if n := want(entry); n > 0 && !entry.Encrypted {
			src, err := f.Open()
			if err != nil {
				return nil, false, fmt.Errorf("archive: open entry %q: %w", f.Name, err)
			}
			p.Head, err = headOf(src, n)
			_ = src.Close()
			if err != nil {
				return nil, false, fmt.Errorf("archive: read entry %q: %w", f.Name, err)
			}
		}
		out = append(out, p)
	}
	return out, false, nil
}

func peek7z(ctx context.Context, r io.ReaderAt, size int64, want func(Entry) int64, maxEntries int) ([]Peeked, bool, error) {
	reader, err := sevenzip.NewReader(r, size)
	if err != nil {
		return nil, false, fmt.Errorf("archive: open 7z: %w", err)
	}
	var out []Peeked
	for _, f := range reader.File {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		if f.FileInfo().IsDir() {
			continue
		}
		if len(out) >= maxEntries {
			return out, true, nil
		}
		entry := Entry{Name: f.Name, SizeBytes: int64(f.UncompressedSize), CRC32: f.CRC32, HasCRC32: true}
		p := Peeked{Entry: entry}
		if n := want(entry); n > 0 {
			src, err := f.Open()
			if err != nil {
				return nil, false, fmt.Errorf("archive: open 7z entry %q: %w", f.Name, err)
			}
			p.Head, err = headOf(src, n)
			_ = src.Close()
			if err != nil {
				return nil, false, fmt.Errorf("archive: read 7z entry %q: %w", f.Name, err)
			}
		}
		out = append(out, p)
	}
	return out, false, nil
}

func peekRar(ctx context.Context, path string, want func(Entry) int64, maxEntries int) ([]Peeked, bool, error) {
	reader, err := rardecode.OpenReader(path)
	if err != nil {
		return nil, false, fmt.Errorf("archive: open rar %s: %w", path, err)
	}
	defer func() { _ = reader.Close() }()
	var out []Peeked
	for {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return out, false, nil
		}
		if err != nil {
			return nil, false, fmt.Errorf("archive: read rar %s: %w", path, err)
		}
		if header.IsDir {
			continue
		}
		if len(out) >= maxEntries {
			return out, true, nil
		}
		entry := Entry{Name: header.Name, SizeBytes: header.UnPackedSize, Encrypted: header.Encrypted}
		p := Peeked{Entry: entry}
		if n := want(entry); n > 0 && !entry.Encrypted {
			if p.Head, err = headOf(reader, n); err != nil {
				return nil, false, fmt.Errorf("archive: read rar entry %q: %w", header.Name, err)
			}
		}
		out = append(out, p)
	}
}

func peekTar(ctx context.Context, r io.Reader, format Format, want func(Entry) int64, maxEntries int) ([]Peeked, bool, error) {
	plain, err := tarReaderFor(r, format)
	if err != nil {
		return nil, false, fmt.Errorf("archive: %s: %w", format, err)
	}
	tr := tar.NewReader(plain)
	var out []Peeked
	for {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return out, false, nil
		}
		if err != nil {
			return nil, false, fmt.Errorf("archive: read tar: %w", err)
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		if len(out) >= maxEntries {
			return out, true, nil
		}
		entry := Entry{Name: header.Name, SizeBytes: header.Size}
		p := Peeked{Entry: entry}
		if n := want(entry); n > 0 {
			if p.Head, err = headOf(tr, n); err != nil {
				return nil, false, fmt.Errorf("archive: read tar entry %q: %w", header.Name, err)
			}
		}
		out = append(out, p)
	}
}
