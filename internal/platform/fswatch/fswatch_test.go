//go:build linux

package fswatch

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func next(t *testing.T, w *Watcher) Event {
	t.Helper()
	select {
	case event, open := <-w.Events():
		if !open {
			t.Fatal("events closed")
		}
		return event
	case <-time.After(3 * time.Second):
		t.Fatal("no event")
		return Event{}
	}
}

func TestWatcher_ReportsAFileAndADirectoryAppearingAndGoing(t *testing.T) {
	root := t.TempDir()
	w, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close() //nolint:errcheck // test cleanup
	if err := w.Add(root); err != nil {
		t.Fatal(err)
	}
	if err := w.Add(root); err != nil || w.Count() != 1 {
		t.Fatalf("adding twice: err %v count %d, want one watch", err, w.Count())
	}

	if err := os.Mkdir(filepath.Join(root, "artist"), 0o755); err != nil {
		t.Fatal(err)
	}
	if event := next(t, w); event != (Event{Dir: root, Name: "artist", IsDir: true, Created: true}) {
		t.Fatalf("mkdir event = %+v", event)
	}
	if err := w.Add(filepath.Join(root, "artist")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "artist", "a.flac"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if event := next(t, w); event.Dir != filepath.Join(root, "artist") || event.Name != "a.flac" || !event.Created {
		t.Fatalf("create event = %+v", event)
	}
	_ = next(t, w) // close_write of the same file
	if err := os.Remove(filepath.Join(root, "artist", "a.flac")); err != nil {
		t.Fatal(err)
	}
	if event := next(t, w); !event.Removed || event.Name != "a.flac" {
		t.Fatalf("remove event = %+v", event)
	}
	w.Remove(filepath.Join(root, "artist"))
	if w.Count() != 1 {
		t.Fatalf("count after remove = %d, want 1", w.Count())
	}
}

func TestWatcher_AMissingDirectoryIsAnErrorNotALimit(t *testing.T) {
	w, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close() //nolint:errcheck // test cleanup
	if err := w.Add(filepath.Join(t.TempDir(), "nope")); err == nil || errors.Is(err, ErrLimit) {
		t.Fatalf("err = %v, want a plain error", err)
	}
}
