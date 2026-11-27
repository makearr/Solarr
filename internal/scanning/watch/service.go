package watch

import (
	"context"
	"errors"
	"log/slog"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fswatch"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// Notifier is the inotify instance the service drives; fswatch.Watcher is one.
type Notifier interface {
	Add(dir string) error
	Remove(dir string)
	// RemoveTree drops a directory and every watch below it.
	RemoveTree(dir string)
	// Watching says whether the directory still has a live watch.
	Watching(dir string) bool
	Count() int
	Events() <-chan fswatch.Event
	Close() error
}

// Root is a root folder the service may watch.
type Root struct {
	ID   int64
	Path string
	// Trusted is false for a root whose changes inotify cannot see all of (a network share changes behind the
	// client's back), so a quiet watcher there proves nothing.
	Trusted bool
}

// DirRecord is a directory that holds library files.
type DirRecord struct {
	Rel     string
	Changed int64
}

// Config wires a Service.
type Config struct {
	Notifier Notifier
	Clock    clock.Clock
	Logger   *slog.Logger
	// Roots are the online root folders of libraries that are scanned.
	Roots func() []Root
	// Directories lists the directories of a root that hold library files, from the library's own records.
	Directories func(ctx context.Context, rootFolderID int64) ([]DirRecord, error)
	// Subdirectories lists the directories directly inside an absolute directory; an unreadable one has none.
	Subdirectories func(absolute string) []string
	// Budget is how many watches Solarr may hold in all.
	Budget func() int
	// Dirty is told which directories of a root changed, once their changes have gone quiet.
	Dirty func(ctx context.Context, rootFolderID int64, relDirs []string)
	// Quiet is how long a directory must be unchanged before it is rescanned; MaxWait bounds a busy one.
	Quiet, MaxWait time.Duration
}

// Service keeps the watches and turns events into directory rescans.
type Service struct {
	config Config
	logger *slog.Logger

	mu           sync.Mutex
	watched      map[string]watchedDir // absolute directory -> where it is in its root
	directories  int                   // directories that hold files, watched or not
	registeredAt map[int64]time.Time
	covered      map[int64]int
	lostAt       time.Time
	dirty        map[dirtyKey]dirtySpan
	budgetShort  bool
	discover     []discovery // new directories whose contents are not watched yet
}

type discovery struct {
	absolute string
	where    watchedDir
}

// maxDiscovered bounds what one burst of new directories may walk; beyond it the ordinary sweep takes over.
const maxDiscovered = 10000

type watchedDir struct {
	root int64
	rel  string
}

type dirtyKey struct {
	root int64
	rel  string
}

type dirtySpan struct{ first, last time.Time }

// New builds a service; nothing is watched until Replan.
func New(config Config) *Service {
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	if config.Quiet <= 0 {
		config.Quiet = 20 * time.Second
	}
	if config.MaxWait <= 0 {
		config.MaxWait = 3 * time.Minute
	}
	return &Service{
		config: config, logger: config.Logger,
		watched: map[string]watchedDir{}, registeredAt: map[int64]time.Time{}, covered: map[int64]int{},
		dirty: map[dirtyKey]dirtySpan{},
	}
}

// Replan lists the directories again and watches as many as the budget allows, shallow first. Watches already held
// stay; a replan only adds, and only forgets directories that are gone from the library's records.
func (s *Service) Replan(ctx context.Context) error {
	var all []Dir
	roots := map[int64]Root{}
	for _, root := range s.config.Roots() {
		roots[root.ID] = root
		all = append(all, Dir{RootFolderID: root.ID, Rel: "", Depth: 0})
		records, err := s.config.Directories(ctx, root.ID)
		if err != nil {
			return err
		}
		for _, record := range records {
			if record.Rel == "" {
				continue
			}
			all = append(all, Dir{RootFolderID: root.ID, Rel: record.Rel, Depth: strings.Count(record.Rel, "/") + 1, Changed: record.Changed})
		}
	}
	budget := s.config.Budget()
	chosen, _ := Plan(all, budget)

	s.mu.Lock()
	s.directories, s.budgetShort = len(all), len(chosen) < len(all)
	s.forgetStaleLocked(roots, all)
	s.mu.Unlock()

	// Registering is a round trip to the disk for each directory on a network share, minutes for a large library:
	// the lock is held only to record, so events and the Disk page are not kept waiting.
	added := map[int64]bool{}
	limitHit := false
	for _, dir := range chosen {
		absolute := path.Join(roots[dir.RootFolderID].Path, dir.Rel)
		s.mu.Lock()
		_, held := s.watched[absolute]
		s.mu.Unlock()
		if held {
			continue
		}
		if err := s.config.Notifier.Add(absolute); err != nil {
			if errors.Is(err, fswatch.ErrLimit) {
				limitHit = true
				break
			}
			s.logger.DebugContext(ctx, "directory not watched", slog.String("path", absolute), slog.Any("error", err))
			continue
		}
		s.mu.Lock()
		s.watched[absolute] = watchedDir{root: dir.RootFolderID, rel: dir.Rel}
		s.mu.Unlock()
		added[dir.RootFolderID] = true
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.config.Clock.Now()
	s.budgetShort = s.budgetShort || limitHit
	for id := range roots {
		if added[id] {
			s.registeredAt[id] = now
		}
		s.covered[id] = coveredFor(all, s.watched, roots[id].Path, id)
	}
	return nil
}

// forgetStaleLocked drops records of watches whose root is gone, or whose directory left the library's records and
// no longer has a live watch.
func (s *Service) forgetStaleLocked(roots map[int64]Root, all []Dir) {
	listed := map[string]bool{}
	for _, dir := range all {
		listed[path.Join(roots[dir.RootFolderID].Path, dir.Rel)] = true
	}
	for absolute, where := range s.watched {
		if _, rooted := roots[where.root]; !rooted {
			s.config.Notifier.RemoveTree(absolute)
			delete(s.watched, absolute)
		} else if !listed[absolute] && !s.config.Notifier.Watching(absolute) {
			delete(s.watched, absolute)
		}
	}
}

func coveredFor(all []Dir, watched map[string]watchedDir, rootPath string, rootID int64) int {
	var mine, chosen []Dir
	for _, dir := range all {
		if dir.RootFolderID != rootID {
			continue
		}
		mine = append(mine, dir)
		if _, held := watched[path.Join(rootPath, dir.Rel)]; held {
			chosen = append(chosen, dir)
		}
	}
	return coveredDepthOf(mine, chosen)
}

// Run reads events until ctx ends; a tick every few seconds hands quiet directories to Dirty.
func (s *Service) Run(ctx context.Context) {
	ticker := s.config.Clock.NewTicker(5 * time.Second)
	defer ticker.Stop()
	events := s.config.Notifier.Events()
	for {
		select {
		case <-ctx.Done():
			return
		case event, open := <-events:
			if !open {
				return
			}
			s.handle(event)
			s.discoverNew(ctx)
		case <-ticker.C():
			s.Flush(ctx)
		}
	}
}

// ignoredName is a file Solarr itself writes while working, which would only make it rescan its own doing.
func ignoredName(name string) bool {
	return fsx.IsInFlightName(name) || strings.HasSuffix(name, ".partial")
}

func (s *Service) handle(event fswatch.Event) {
	now := s.config.Clock.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if event.Overflow {
		s.lostAt = now
		return
	}
	where, known := s.watched[event.Dir]
	if !known || ignoredName(event.Name) {
		return
	}
	if event.IsDir && event.Removed {
		s.forgetTreeLocked(path.Join(event.Dir, event.Name))
	}
	s.markLocked(dirtyKey(where), now)
	if event.IsDir && event.Created {
		child := path.Join(event.Dir, event.Name)
		s.discover = append(s.discover, discovery{child, watchedDir{where.root, path.Join(where.rel, event.Name)}})
	}
}

// discoverNew watches a copied-in tree down to its leaves: a directory is watched before it is listed, so a
// subdirectory created in between still produces an event. Directories past the budget are rescanned once, unwatched.
func (s *Service) discoverNew(ctx context.Context) {
	s.mu.Lock()
	queue := s.discover
	s.discover = nil
	s.mu.Unlock()
	for visited := 0; len(queue) > 0; visited++ {
		next := queue[0]
		queue = queue[1:]
		if visited >= maxDiscovered || ctx.Err() != nil {
			s.mu.Lock()
			s.lostAt = s.config.Clock.Now()
			s.mu.Unlock()
			return
		}
		s.mu.Lock()
		_, held := s.watched[next.absolute]
		room := s.config.Notifier.Count() < s.config.Budget()
		s.mu.Unlock()
		watching := held
		if !held && room && s.config.Notifier.Add(next.absolute) == nil {
			watching = true
		}
		s.mu.Lock()
		if watching {
			s.watched[next.absolute] = next.where
		} else {
			s.budgetShort = true
		}
		s.markLocked(dirtyKey(next.where), s.config.Clock.Now())
		s.mu.Unlock()
		if s.config.Subdirectories == nil {
			continue
		}
		for _, name := range s.config.Subdirectories(next.absolute) {
			queue = append(queue, discovery{path.Join(next.absolute, name), watchedDir{next.where.root, path.Join(next.where.rel, name)}})
		}
	}
}

// forgetTreeLocked drops the watches below a directory that went or was renamed: inotify follows the inode, so they
// would keep reporting the old name. A moved-in directory comes back as a creation and is watched under its new name.
func (s *Service) forgetTreeLocked(dir string) {
	s.config.Notifier.RemoveTree(dir)
	for absolute, where := range s.watched {
		if absolute == dir || strings.HasPrefix(absolute, dir+"/") {
			delete(s.watched, absolute)
			delete(s.dirty, dirtyKey(where))
		}
	}
}

func (s *Service) markLocked(key dirtyKey, now time.Time) {
	span, pending := s.dirty[key]
	if !pending {
		span.first = now
	}
	span.last = now
	s.dirty[key] = span
}

// Flush hands every directory that has been quiet (or waiting too long) to Dirty.
func (s *Service) Flush(ctx context.Context) {
	now := s.config.Clock.Now()
	ready := map[int64][]string{}
	s.mu.Lock()
	for key, span := range s.dirty {
		if now.Sub(span.last) >= s.config.Quiet || now.Sub(span.first) >= s.config.MaxWait {
			ready[key.root] = append(ready[key.root], key.rel)
			delete(s.dirty, key)
		}
	}
	s.mu.Unlock()
	for root, dirs := range ready {
		s.config.Dirty(ctx, root, dirs)
	}
}

// Status is what the Disk page shows.
type Status struct {
	Watches     int
	Directories int
	BudgetShort bool
}

// Status reports the watches held and the directories they were chosen from.
func (s *Service) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Status{Watches: len(s.watched), Directories: s.directories, BudgetShort: s.budgetShort}
}

// Trust says whether the watch on a root has seen every change since lastScan, and how deep it reaches. It is false
// for a root on a share the watcher cannot see through, one registered after the scan, or after events were lost.
func (s *Service) Trust(root Root, lastScan time.Time) (watching bool, coveredDepth int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	registered, has := s.registeredAt[root.ID]
	if !root.Trusted || !has || registered.After(lastScan) || s.lostAt.After(lastScan) {
		return false, -1
	}
	return true, s.covered[root.ID]
}

// Registered says the root has live watches, whether or not they are yet trusted to vouch for the last scan.
func (s *Service) Registered(rootID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, has := s.registeredAt[rootID]
	return has
}

// Close stops watching.
func (s *Service) Close() error { return s.config.Notifier.Close() }
