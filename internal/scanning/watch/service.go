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
)

// Notifier is the inotify instance the service drives; fswatch.Watcher is one.
type Notifier interface {
	Add(dir string) error
	Remove(dir string)
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
}

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
		case <-ticker.C():
			s.Flush(ctx)
		}
	}
}

// ignoredName is a file Solarr itself writes while working, which would only make it rescan its own doing.
func ignoredName(name string) bool {
	return strings.HasSuffix(name, ".mnx-move-tmp") || strings.HasSuffix(name, ".partial") || strings.HasPrefix(name, ".mnx-write-probe")
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
		delete(s.watched, path.Join(event.Dir, event.Name))
	}
	s.markLocked(dirtyKey(where), now)
	if event.IsDir && event.Created {
		child := path.Join(event.Dir, event.Name)
		if s.config.Notifier.Count() < s.config.Budget() && s.config.Notifier.Add(child) == nil {
			childRel := path.Join(where.rel, event.Name)
			s.watched[child] = watchedDir{root: where.root, rel: childRel}
			s.markLocked(dirtyKey{where.root, childRel}, now)
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

// Close stops watching.
func (s *Service) Close() error { return s.config.Notifier.Close() }
