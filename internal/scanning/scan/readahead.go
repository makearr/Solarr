package scan

import (
	"context"
	"errors"
	"io/fs"
	"path"
	"slices"
	"strings"
	"sync"

	"github.com/makearr/Solarr/internal/platform/fsx"
)

// readAheadWorkers is how many directories are read at once ahead of the walk: on cold NFS every read is a round trip,
// and sixteen in flight walked a real library tree 15 times faster than one (measured 2026-11-03).
const readAheadWorkers = 16

// readAheadLimit bounds the directories read but not yet walked, so a wide root is never held in memory whole.
const readAheadLimit = 512

// listing is one directory as the walk needs it, read by a worker or by the walk itself.
type listing struct {
	relative string
	started  bool
	done     chan struct{}

	// gone is a directory that vanished before its stat: the walk skips it without counting a visit.
	gone    bool
	info    fsx.FileInfo
	entries []fsx.DirEntry
	statErr error
	readErr error
	// files are the included files with their stats, present only when the walk reads the directory.
	files []listedFile
}

type listedFile struct {
	name string
	info fsx.FileInfo
	err  error
}

// readAhead reads directories in the order the walk will ask for them; the walk still takes them strictly in order.
type readAhead struct {
	fsys    fsx.FS
	root    string
	include func(relPath string) bool
	// reads answers whether the walk will read a directory's files, so their stats are taken here too.
	reads func(relative string, info fsx.FileInfo) bool

	mu      sync.Mutex
	wake    *sync.Cond
	known   map[string]*listing
	pending []*listing // unstarted, the one the walk needs soonest last
	busy    int        // started or finished by a worker, not yet taken
	closed  bool
	workers sync.WaitGroup
}

func newReadAhead(ctx context.Context, w *sweep) *readAhead {
	r := &readAhead{
		fsys: w.sweeper.fsys, root: w.root, include: w.options.Include, reads: w.reads,
		known: map[string]*listing{},
	}
	r.wake = sync.NewCond(&r.mu)
	for range readAheadWorkers {
		r.workers.Add(1)
		go r.work(ctx)
	}
	return r
}

// schedule queues a directory's subdirectories, which the walk visits before anything already queued.
func (r *readAhead) schedule(children []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, relative := range slices.Backward(children) {
		next := &listing{relative: relative, done: make(chan struct{})}
		r.known[relative] = next
		r.pending = append(r.pending, next)
	}
	r.wake.Broadcast()
}

// take answers a scheduled directory, reading it here when no worker has started it yet.
func (r *readAhead) take(ctx context.Context, relative string) *listing {
	r.mu.Lock()
	next := r.known[relative]
	delete(r.known, relative)
	if next.started {
		r.mu.Unlock()
		<-next.done
		r.mu.Lock()
		r.busy--
		r.wake.Broadcast()
		r.mu.Unlock()
		return next
	}
	next.started = true
	if index := slices.Index(r.pending, next); index >= 0 {
		r.pending = slices.Delete(r.pending, index, index+1)
	}
	r.mu.Unlock()
	r.fill(ctx, next)
	return next
}

func (r *readAhead) work(ctx context.Context) {
	defer r.workers.Done()
	for {
		r.mu.Lock()
		for !r.closed && (len(r.pending) == 0 || r.busy >= readAheadLimit) {
			r.wake.Wait()
		}
		if r.closed {
			r.mu.Unlock()
			return
		}
		next := r.pending[len(r.pending)-1]
		r.pending = r.pending[:len(r.pending)-1]
		next.started = true
		r.busy++
		r.mu.Unlock()
		r.fill(ctx, next)
	}
}

// close stops the workers and waits for the reads in flight, so nothing touches the filesystem after the sweep.
func (r *readAhead) close() {
	r.mu.Lock()
	r.closed = true
	r.wake.Broadcast()
	r.mu.Unlock()
	r.workers.Wait()
}

func (r *readAhead) fill(ctx context.Context, next *listing) {
	defer close(next.done)
	if next.statErr = ctx.Err(); next.statErr != nil {
		return
	}
	absolute := r.root
	if next.relative != "" {
		absolute = path.Join(r.root, next.relative)
	}
	info, err := r.fsys.Stat(absolute)
	if errors.Is(err, fs.ErrNotExist) {
		next.gone = true
		return
	}
	if err != nil {
		next.statErr = err
		return
	}
	next.info = info
	entries, err := r.fsys.ReadDir(absolute)
	if err != nil {
		next.readErr = err
		return
	}
	// Lexical order is what makes Cursor enough to resume: the walk has to visit directories in the same order every time.
	slices.SortFunc(entries, func(a, b fsx.DirEntry) int { return strings.Compare(a.Name, b.Name) })
	next.entries = entries
	if !r.reads(next.relative, info) {
		return
	}
	for _, entry := range entries {
		if entry.IsDir || (r.include != nil && !r.include(path.Join(next.relative, entry.Name))) {
			continue
		}
		child, err := r.fsys.Stat(path.Join(absolute, entry.Name))
		next.files = append(next.files, listedFile{name: entry.Name, info: child, err: err})
	}
}
