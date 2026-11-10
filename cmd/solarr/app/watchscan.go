package app

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fswatch"
	"github.com/makearr/Solarr/internal/scanning/scan"
	"github.com/makearr/Solarr/internal/scanning/watch"
)

// replanEvery is how often the watched directories are chosen again from the library's records, which reads no disk.
const replanEvery = 30 * time.Minute

// scannedRoots are the online root folders of libraries whose media type is scanned, each with the libraries on it.
func (p *process) scannedRoots() map[int64][]int64 {
	snapshot := p.cache.Snapshot()
	roots := map[int64][]int64{}
	for _, category := range snapshot.Categories() {
		if _, scanned := media.Capability[media.Scanning](p.media, media.Key(category.MediaType)); !scanned {
			continue
		}
		for _, root := range snapshot.RootFoldersForCategory(category.ID) {
			if root.Online {
				roots[root.ID] = append(roots[root.ID], category.ID)
			}
		}
	}
	return roots
}

// startLibraryWatch watches the library folders with inotify, within half of the host's watch limit, and turns
// changes into rescans of just the directories that changed. Without a limit it can read, it does nothing.
func (p *process) startLibraryWatch(ctx context.Context) {
	budget := p.disks.WatchBudget()
	if budget <= 0 {
		p.logger.Info("folder watching is off: the host's inotify watch limit is unreadable or zero")
		return
	}
	notifier, err := fswatch.New()
	if err != nil {
		p.logger.Warn("folder watching is off", slog.Any("error", err))
		return
	}
	physical := library.NewPhysical(p.library)
	service := watch.New(watch.Config{
		Notifier: notifier, Clock: clock.NewSystem(), Logger: p.logger,
		Roots: func() []watch.Root {
			var roots []watch.Root
			for id := range p.scannedRoots() {
				if root, found := p.cache.Snapshot().RootFolder(id); found {
					roots = append(roots, watch.Root{ID: id, Path: root.Path, Trusted: p.disks.WatchSeesChanges(root.Path)})
				}
			}
			return roots
		},
		Directories: func(ctx context.Context, rootFolderID int64) ([]watch.DirRecord, error) {
			records, err := physical.DirectoriesOfRoot(ctx, rootFolderID)
			out := make([]watch.DirRecord, len(records))
			for i, record := range records {
				out[i] = watch.DirRecord{Rel: record.Rel, Changed: record.Changed}
			}
			return out, err
		},
		Budget: func() int { return budget },
		Dirty:  p.rescanDirectories,
	})
	p.watch = service
	p.disks.WatchInUse = func() (int64, int64) {
		status := service.Status()
		return int64(status.Watches), int64(status.Directories)
	}
	go func() {
		defer service.Close() //nolint:errcheck // shutting down
		if err := service.Replan(ctx); err != nil {
			p.logger.Warn("choosing the folders to watch", slog.Any("error", err))
		}
		go service.Run(ctx)
		ticker := clock.NewSystem().NewTicker(replanEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C():
				if err := service.Replan(ctx); err != nil {
					p.logger.Warn("choosing the folders to watch", slog.Any("error", err))
				}
			}
		}
	}()
	p.logger.Info("folder watching started", slog.Int("watch_budget", budget))
}

// rescanDirectories reads again the directories of a root that changed, in each library that uses the root.
func (p *process) rescanDirectories(ctx context.Context, rootFolderID int64, relDirs []string) {
	for _, categoryID := range p.scannedRoots()[rootFolderID] {
		if _, err := scan.EnqueueDirectories(ctx, p.queue, rootFolderID, categoryID, relDirs); err != nil {
			p.logger.Warn("enqueue rescan of changed folders",
				slog.Int64("root_folder_id", rootFolderID), slog.Any("error", err))
		}
	}
}

// scanWhy is what the policy decided for one root, for logs and the Disk page.
type scanWhy struct {
	Due          bool
	Why          watch.Why
	Watching     bool
	CoveredDepth int
	LastScan     time.Time
}

// scanDecision applies the schedule to one root: manual and startup runs, the person's interval, what the watcher
// vouches for and, last, a cheap look at the disk.
func (p *process) scanDecision(ctx context.Context, rootID int64, manual, startup, lookAtDisk bool) scanWhy {
	root, found := p.cache.Snapshot().RootFolder(rootID)
	if !found {
		return scanWhy{}
	}
	progress, err := library.NewScans(p.library).Progress(ctx, rootID)
	if err != nil {
		return scanWhy{Due: true, Why: watch.WhyNeverScanned}
	}
	input := watch.Input{
		Now: clock.NewSystem().Now(), LastScan: progress.FinishedAt, Manual: manual, Startup: startup,
		Interval: p.cache.Snapshot().ScanInterval(),
	}
	if p.watch != nil {
		input.Watching, input.CoveredDepth = p.watch.Trust(watch.Root{ID: rootID, Path: root.Path, Trusted: p.disks.WatchSeesChanges(root.Path)}, progress.FinishedAt)
	}
	due, why := watch.ScanDue(input)
	if lookAtDisk && !due && !input.Watching && !manual && !startup && input.Interval == 0 {
		input.Fingerprint = p.fingerprintVerdict(root)
		due, why = watch.ScanDue(input)
	}
	return scanWhy{Due: due, Why: why, Watching: input.Watching, CoveredDepth: input.CoveredDepth, LastScan: progress.FinishedAt}
}

// fingerprintVerdict compares the root's used space and top-level entry count with the last scan's.
func (p *process) fingerprintVerdict(root settings.RootFolder) watch.Fingerprint {
	stored, known := p.cache.Snapshot().ScanFingerprints()[root.ID]
	current, ok := p.rootFingerprint(root)
	switch {
	case !known || !ok:
		return watch.FingerprintUnknown
	case stored == current:
		return watch.FingerprintSame
	default:
		return watch.FingerprintChanged
	}
}

func (p *process) rootFingerprint(root settings.RootFolder) (string, bool) {
	total, free, err := p.fsys.DiskUsage(root.Path)
	if err != nil {
		return "", false
	}
	entries, err := p.fsys.ReadDir(root.Path)
	if err != nil {
		return "", false
	}
	return fmt.Sprintf("used=%d;entries=%d", total-free, len(entries)), true
}

// rememberFingerprint stores what the root looks like as its scan is queued.
func (p *process) rememberFingerprint(ctx context.Context, root settings.RootFolder) {
	if current, ok := p.rootFingerprint(root); ok {
		if err := p.cache.SetScanFingerprint(ctx, clock.NewSystem(), root.ID, current); err != nil {
			p.logger.Warn("remember the root's size", slog.Int64("root_folder_id", root.ID), slog.Any("error", err))
		}
	}
}

// diskScanRoots tells the Disk page what the schedule thinks of each root, without touching any disk.
func (p *process) diskScanRoots(ctx context.Context) []apiserver.DiskScanRoot {
	var rows []apiserver.DiskScanRoot
	for id := range p.scannedRoots() {
		root, found := p.cache.Snapshot().RootFolder(id)
		if !found {
			continue
		}
		decision := p.scanDecision(ctx, id, false, false, false)
		row := apiserver.DiskScanRoot{
			RootFolderID: id, Path: root.Path, Watching: decision.Watching, CoveredDepth: decision.CoveredDepth,
			Due: decision.Due, Why: string(decision.Why),
		}
		if !decision.LastScan.IsZero() {
			row.LastScanUnix = decision.LastScan.Unix()
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Path < rows[j].Path })
	return rows
}
