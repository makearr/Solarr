package fsx

import (
	"errors"
	"io"
	"os"
	"syscall"
)

// directBlock is the alignment O_DIRECT asks of offsets and lengths on a local filesystem.
const directBlock = 4096

// ReadSpans reads with O_DIRECT, falling back to ordinary reads where the filesystem refuses it.
func (OS) ReadSpans(path string, spans []Span) ([][]byte, error) {
	read, err := retryStale(func() ([][]byte, error) { return readSpans(path, spans, syscall.O_DIRECT) })
	if errors.Is(err, syscall.EINVAL) {
		return retryStale(func() ([][]byte, error) { return readSpans(path, spans, 0) })
	}
	return read, err
}

func readSpans(path string, spans []Span, flags int) ([][]byte, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|flags, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }() // a read handle; a failed close loses nothing
	return spansFrom(file, spans)
}

// spansFrom reads each span at block-aligned bounds, so the same reads satisfy O_DIRECT.
func spansFrom(file io.ReaderAt, spans []Span) ([][]byte, error) {
	read := make([][]byte, len(spans))
	for index, span := range spans {
		start := span.Offset &^ (directBlock - 1)
		end := span.Offset + int64(span.Length)
		buffer := make([]byte, (end-start+directBlock-1)&^(directBlock-1))
		count, err := file.ReadAt(buffer, start)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		skip := int(span.Offset - start)
		read[index] = buffer[min(skip, count):min(count, skip+span.Length)]
	}
	return read, nil
}
