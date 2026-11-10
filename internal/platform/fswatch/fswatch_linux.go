// Package fswatch reports changes inside watched directories through inotify. A watch covers one directory, not its
// subdirectories, so a caller that wants a tree adds each directory itself and sees how many watches that costs.
package fswatch

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"unsafe"
)

// ErrLimit means the host's inotify watch limit is used up.
var ErrLimit = errors.New("fswatch: the host's inotify watch limit is reached")

// Event is one change under a watched directory; Overflow means events were lost and every watch is in doubt.
type Event struct {
	Dir      string
	Name     string
	IsDir    bool
	Created  bool // a file or directory appeared (created or moved in)
	Removed  bool // a file or directory went (deleted or moved out)
	Overflow bool
}

const watchMask = syscall.IN_CREATE | syscall.IN_CLOSE_WRITE | syscall.IN_DELETE | syscall.IN_MOVED_FROM |
	syscall.IN_MOVED_TO | syscall.IN_DELETE_SELF | syscall.IN_MOVE_SELF | syscall.IN_ONLYDIR

// Watcher is one inotify instance.
type Watcher struct {
	file   *os.File
	events chan Event

	mu     sync.Mutex
	byPath map[string]int
	byID   map[int]string
	closed bool
}

// New opens an inotify instance and starts reading it.
func New() (*Watcher, error) {
	fd, err := syscall.InotifyInit1(syscall.IN_CLOEXEC | syscall.IN_NONBLOCK)
	if err != nil {
		return nil, fmt.Errorf("fswatch: init inotify: %w", err)
	}
	w := &Watcher{
		file: os.NewFile(uintptr(fd), "inotify"), events: make(chan Event, 1024),
		byPath: map[string]int{}, byID: map[int]string{},
	}
	go w.read()
	return w, nil
}

// Events delivers what happened; it is closed after Close. A reader that falls behind loses events, which is then
// reported as one Overflow.
func (w *Watcher) Events() <-chan Event { return w.events }

// Add watches a directory; watching one already watched is not an error.
func (w *Watcher) Add(dir string) error {
	dir = filepath.Clean(dir)
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return os.ErrClosed
	}
	if _, watched := w.byPath[dir]; watched {
		return nil
	}
	id, err := syscall.InotifyAddWatch(int(w.file.Fd()), dir, watchMask)
	if errors.Is(err, syscall.ENOSPC) {
		return ErrLimit
	}
	if err != nil {
		return fmt.Errorf("fswatch: watch %s: %w", dir, err)
	}
	if old, reused := w.byID[id]; reused && old != dir {
		delete(w.byPath, old)
	}
	w.byPath[dir], w.byID[id] = id, dir
	return nil
}

// Remove stops watching a directory.
func (w *Watcher) Remove(dir string) {
	dir = filepath.Clean(dir)
	w.mu.Lock()
	defer w.mu.Unlock()
	if id, watched := w.byPath[dir]; watched {
		_, _ = syscall.InotifyRmWatch(int(w.file.Fd()), uint32(id))
		delete(w.byPath, dir)
		delete(w.byID, id)
	}
}

// Count is how many directories are watched.
func (w *Watcher) Count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.byPath)
}

// Close stops watching and closes Events.
func (w *Watcher) Close() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	w.mu.Unlock()
	return w.file.Close()
}

func (w *Watcher) read() {
	defer close(w.events)
	buffer := make([]byte, 64*1024)
	for {
		n, err := w.file.Read(buffer)
		if err != nil {
			return
		}
		for offset := 0; offset+syscall.SizeofInotifyEvent <= n; {
			raw := (*syscall.InotifyEvent)(unsafe.Pointer(&buffer[offset]))
			name := ""
			if raw.Len > 0 {
				bytes := buffer[offset+syscall.SizeofInotifyEvent : offset+syscall.SizeofInotifyEvent+int(raw.Len)]
				for i, b := range bytes {
					if b == 0 {
						bytes = bytes[:i]
						break
					}
				}
				name = string(bytes)
			}
			offset += syscall.SizeofInotifyEvent + int(raw.Len)
			w.deliver(raw, name)
		}
	}
}

func (w *Watcher) deliver(raw *syscall.InotifyEvent, name string) {
	if raw.Mask&syscall.IN_Q_OVERFLOW != 0 {
		w.send(Event{Overflow: true})
		return
	}
	w.mu.Lock()
	dir, known := w.byID[int(raw.Wd)]
	if raw.Mask&syscall.IN_IGNORED != 0 {
		delete(w.byID, int(raw.Wd))
		delete(w.byPath, dir)
	}
	w.mu.Unlock()
	if !known || raw.Mask&syscall.IN_IGNORED != 0 {
		return
	}
	w.send(Event{
		Dir: dir, Name: name, IsDir: raw.Mask&syscall.IN_ISDIR != 0,
		Created: raw.Mask&(syscall.IN_CREATE|syscall.IN_MOVED_TO) != 0,
		Removed: raw.Mask&(syscall.IN_DELETE|syscall.IN_MOVED_FROM|syscall.IN_DELETE_SELF|syscall.IN_MOVE_SELF) != 0,
	})
}

func (w *Watcher) send(event Event) {
	select {
	case w.events <- event:
	default:
		// The reader is behind: drop this one and leave a single overflow marker if there is room.
		select {
		case w.events <- Event{Overflow: true}:
		default:
		}
	}
}
