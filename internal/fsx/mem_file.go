package fsx

import (
	"io"
	"io/fs"
)

// memFile reads through to the live content rather than a snapshot, so a write
// through one path of a hardlink is visible from the other.
type memFile struct {
	content *memContent
	offset  int64
	closed  bool
}

func (f *memFile) Read(p []byte) (int, error) {
	if f.closed {
		return 0, fs.ErrClosed
	}
	f.content.mu.Lock()
	defer f.content.mu.Unlock()
	if f.offset >= int64(len(f.content.data)) {
		return 0, io.EOF
	}
	n := copy(p, f.content.data[f.offset:])
	f.offset += int64(n)
	return n, nil
}

func (f *memFile) ReadAt(p []byte, off int64) (int, error) {
	if f.closed {
		return 0, fs.ErrClosed
	}
	if off < 0 {
		return 0, io.ErrUnexpectedEOF
	}
	f.content.mu.Lock()
	defer f.content.mu.Unlock()
	if off >= int64(len(f.content.data)) {
		return 0, io.EOF
	}
	n := copy(p, f.content.data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (f *memFile) Seek(offset int64, whence int) (int64, error) {
	if f.closed {
		return 0, fs.ErrClosed
	}
	f.content.mu.Lock()
	size := int64(len(f.content.data))
	f.content.mu.Unlock()

	var absolute int64
	switch whence {
	case io.SeekStart:
		absolute = offset
	case io.SeekCurrent:
		absolute = f.offset + offset
	case io.SeekEnd:
		absolute = size + offset
	default:
		return 0, fs.ErrInvalid
	}
	if absolute < 0 {
		return 0, fs.ErrInvalid
	}
	f.offset = absolute
	return absolute, nil
}

func (f *memFile) Close() error {
	f.closed = true
	return nil
}

// memWriter appends to the node's content, so a partially written file is
// visible while it is being written, as it is on a real filesystem.
type memWriter struct {
	content *memContent
	closed  bool
}

func (w *memWriter) Write(p []byte) (int, error) {
	if w.closed {
		return 0, fs.ErrClosed
	}
	w.content.mu.Lock()
	defer w.content.mu.Unlock()
	w.content.data = append(w.content.data, p...)
	return len(p), nil
}

func (w *memWriter) Close() error {
	w.closed = true
	return nil
}
