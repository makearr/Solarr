package fsx

import (
	"io"
	"io/fs"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
)

// Meter is told how long Solarr's own filesystem calls take; it never causes a call.
type Meter interface {
	// Latency is one call that answers without moving much data.
	Latency(path string, d time.Duration)
	// Transfer is bytes read or written and the time the calls took.
	Transfer(path string, bytes int64, d time.Duration)
}

// Metered is an FS that reports the timing of what passes through it.
type Metered struct {
	FS
	meter Meter
	clock clock.Clock
}

// NewMetered wraps inner.
func NewMetered(inner FS, c clock.Clock, meter Meter) Metered {
	return Metered{FS: inner, meter: meter, clock: c}
}

func (m Metered) timed(path string, call func()) {
	started := m.clock.Now()
	call()
	m.meter.Latency(path, m.clock.Now().Sub(started))
}

// Stat is timed.
func (m Metered) Stat(path string) (info FileInfo, err error) {
	m.timed(path, func() { info, err = m.FS.Stat(path) })
	return info, err
}

// Lstat is timed.
func (m Metered) Lstat(path string) (info FileInfo, err error) {
	m.timed(path, func() { info, err = m.FS.Lstat(path) })
	return info, err
}

// ReadDir is timed.
func (m Metered) ReadDir(path string) (entries []DirEntry, err error) {
	m.timed(path, func() { entries, err = m.FS.ReadDir(path) })
	return entries, err
}

// Rename is timed against the destination.
func (m Metered) Rename(oldPath, newPath string) (err error) {
	m.timed(newPath, func() { err = m.FS.Rename(oldPath, newPath) })
	return err
}

// Remove is timed.
func (m Metered) Remove(path string) (err error) {
	m.timed(path, func() { err = m.FS.Remove(path) })
	return err
}

// Link is timed against the new name.
func (m Metered) Link(oldPath, newPath string) (err error) {
	m.timed(newPath, func() { err = m.FS.Link(oldPath, newPath) })
	return err
}

// MkdirAll is timed.
func (m Metered) MkdirAll(path string, perm fs.FileMode) (err error) {
	m.timed(path, func() { err = m.FS.MkdirAll(path, perm) })
	return err
}

// Open times the open and the reads that follow.
func (m Metered) Open(path string) (file File, err error) {
	m.timed(path, func() { file, err = m.FS.Open(path) })
	if err != nil {
		return nil, err
	}
	return meteredFile{File: file, path: path, m: m}, nil
}

// Create times the writes that follow.
func (m Metered) Create(path string) (io.WriteCloser, error) {
	w, err := m.FS.Create(path)
	if err != nil {
		return nil, err
	}
	return meteredWriter{WriteCloser: w, path: path, m: m}, nil
}

type meteredFile struct {
	File
	path string
	m    Metered
}

func (f meteredFile) Read(p []byte) (int, error) {
	started := f.m.clock.Now()
	n, err := f.File.Read(p)
	f.m.meter.Transfer(f.path, int64(n), f.m.clock.Now().Sub(started))
	return n, err
}

func (f meteredFile) ReadAt(p []byte, off int64) (int, error) {
	started := f.m.clock.Now()
	n, err := f.File.ReadAt(p, off)
	f.m.meter.Transfer(f.path, int64(n), f.m.clock.Now().Sub(started))
	return n, err
}

type meteredWriter struct {
	io.WriteCloser
	path string
	m    Metered
}

func (w meteredWriter) Write(p []byte) (int, error) {
	started := w.m.clock.Now()
	n, err := w.WriteCloser.Write(p)
	w.m.meter.Transfer(w.path, int64(n), w.m.clock.Now().Sub(started))
	return n, err
}
