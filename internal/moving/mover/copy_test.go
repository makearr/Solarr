package mover_test

import (
	"context"
	"errors"
	"io"
	"path"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/moving/mover"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// instrumentedFS counts opens per path, records flushes of created files and can break a read half way.
type instrumentedFS struct {
	fsx.FS
	opens        map[string]int
	synced       int
	created      []string
	failReadsOf  string
	errReadBreak error
}

func (f *instrumentedFS) Open(path string) (fsx.File, error) {
	f.opens[path]++
	file, err := f.FS.Open(path)
	if err != nil || path != f.failReadsOf {
		return file, err
	}
	return &breakingFile{File: file, err: f.errReadBreak}, nil
}

func (f *instrumentedFS) Create(path string) (io.WriteCloser, error) {
	f.created = append(f.created, path)
	writer, err := f.FS.Create(path)
	if err != nil {
		return nil, err
	}
	return &syncingWriter{WriteCloser: writer, fs: f}, nil
}

type breakingFile struct {
	fsx.File
	err  error
	read bool
}

func (b *breakingFile) Read(p []byte) (int, error) {
	if b.read {
		return 0, b.err
	}
	b.read = true
	return b.File.Read(p[:3])
}

type syncingWriter struct {
	io.WriteCloser
	fs *instrumentedFS
}

func (w *syncingWriter) Sync() error { w.fs.synced++; return nil }

func crossFilesystemFixture(t *testing.T) (*instrumentedFS, *fsx.Mem) {
	t.Helper()
	mem := fsx.NewMem(clock.NewFakeAtEpoch())
	writeFile(t, mem, "/downloads/Old/track.flac", "the bytes")
	return &instrumentedFS{FS: mem, opens: map[string]int{}}, mem
}

func leftovers(t *testing.T, mem *fsx.Mem) []string {
	t.Helper()
	entries, err := mem.ReadDir("/library/New")
	if err != nil {
		return nil
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	return names
}

func TestCopy_ABrokenReadLeavesNothingInTheLibrary(t *testing.T) {
	fsys, mem := crossFilesystemFixture(t)
	fsys.failReadsOf = "/downloads/Old/track.flac"
	fsys.errReadBreak = errors.New("connection lost")

	_, err := mover.PlaceOne(t.Context(), fsys, "/downloads/Old/track.flac", "/library/New/track.flac", int64(len("the bytes")), []mover.PlaceMethod{mover.PlaceCopy})
	if err == nil {
		t.Fatal("PlaceOne succeeded on a broken read")
	}
	if names := leftovers(t, mem); len(names) != 0 {
		t.Errorf("library folder holds %v, want nothing (no final name, no temporary name)", names)
	}
}

func TestCopy_ACancelledContextStopsAndLeavesNothing(t *testing.T) {
	fsys, mem := crossFilesystemFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := mover.PlaceOne(ctx, fsys, "/downloads/Old/track.flac", "/library/New/track.flac", int64(len("the bytes")), []mover.PlaceMethod{mover.PlaceCopy})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("PlaceOne = %v, want context.Canceled", err)
	}
	if names := leftovers(t, mem); len(names) != 0 {
		t.Errorf("library folder holds %v, want nothing", names)
	}
}

func TestCopy_ReadsTheSourceOnceAndFlushesBeforeReturning(t *testing.T) {
	fsys, mem := crossFilesystemFixture(t)

	if _, err := mover.PlaceOne(t.Context(), fsys, "/downloads/Old/track.flac", "/library/New/track.flac", int64(len("the bytes")), []mover.PlaceMethod{mover.PlaceCopy}); err != nil {
		t.Fatalf("PlaceOne: %v", err)
	}
	if got := fsys.opens["/downloads/Old/track.flac"]; got != 1 {
		t.Errorf("source opened %d times, want 1 (hash while copying)", got)
	}
	if fsys.synced != 1 {
		t.Errorf("copied file flushed %d times, want 1", fsys.synced)
	}
	if names := leftovers(t, mem); len(names) != 1 || names[0] != "track.flac" {
		t.Errorf("library folder holds %v, want only track.flac", names)
	}
	for _, name := range leftovers(t, mem) {
		if strings.HasPrefix(name, mover.TempPrefix) {
			t.Errorf("temporary file %q left behind", name)
		}
	}
}

func TestCopy_BytesAreWrittenUnderAnInFlightNameNeverTheFinalOne(t *testing.T) {
	fsys, _ := crossFilesystemFixture(t)

	if _, err := mover.PlaceOne(t.Context(), fsys, "/downloads/Old/track.flac", "/library/New/track.flac", int64(len("the bytes")), []mover.PlaceMethod{mover.PlaceCopy}); err != nil {
		t.Fatalf("PlaceOne: %v", err)
	}
	if len(fsys.created) != 1 || fsys.created[0] == "/library/New/track.flac" || !strings.HasPrefix(path.Base(fsys.created[0]), mover.TempPrefix) {
		t.Errorf("files created = %v, want one in-flight name next to the final one", fsys.created)
	}
}
