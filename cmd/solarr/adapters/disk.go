package adapters

import (
	"bufio"
	"context"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/disklatency"
	"github.com/makearr/Solarr/internal/platform/disktopo"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/scanning/watch"
)

const inotifyLimitFile = "/proc/sys/fs/inotify/max_user_watches"

// DiskManager satisfies apiserver.DiskManager over disktopo, disklatency and the stored corrections.
type DiskManager struct {
	FS      fsx.FS
	Cache   *settings.Cache
	Clock   clock.Clock
	Monitor *disklatency.Monitor
	// ExtraPaths are folders that are not root folders but still use a disk (the config folder).
	ExtraPaths []string
	// ScanRoots says how each root stands with the scan schedule; nil means none.
	ScanRoots func(ctx context.Context) []apiserver.DiskScanRoot
	// WatchInUse says how many inotify watches Solarr holds and how many directories they were chosen from; nil means none.
	WatchInUse func() (watches, directories int64)

	mu       sync.Mutex
	cached   []apiserver.DiskPath
	cachedAt time.Time
	// pathGroups remembers paths outside every root folder.
	pathGroups map[string]pathGroup
}

// layoutTTL is how long a detected layout is reused by the per-job lookups; mounts rarely change.
const layoutTTL = time.Minute

// GroupOfRoot is the disk group and path of a root folder, from a layout at most layoutTTL old.
func (a *DiskManager) GroupOfRoot(rootFolderID int64) (group, path string, found bool) {
	var rootPath string
	for _, root := range a.Cache.Snapshot().RootFolders() {
		if root.ID == rootFolderID {
			rootPath = root.Path
		}
	}
	if rootPath == "" {
		return "", "", false
	}
	for _, row := range a.cachedLayouts() {
		if row.Path == rootPath {
			return row.Group, row.Path, true
		}
	}
	return "", "", false
}

// GroupOfPath is the disk group of any path: a root folder's group when the path lies under one, otherwise detected
// on the spot (a download folder, a recycle bin) and remembered for a layoutTTL.
func (a *DiskManager) GroupOfPath(path string) string {
	best, bestLength := "", -1
	for _, row := range a.cachedLayouts() {
		if (path == row.Path || strings.HasPrefix(path, strings.TrimSuffix(row.Path, "/")+"/")) && len(row.Path) > bestLength {
			best, bestLength = row.Group, len(row.Path)
		}
	}
	if bestLength >= 0 {
		return best
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if known, ok := a.pathGroups[path]; ok && a.Clock.Now().Sub(known.at) <= layoutTTL {
		return known.group
	}
	group := disktopo.Detect(a.FS, []string{path})[0].Group()
	if a.pathGroups == nil {
		a.pathGroups = map[string]pathGroup{}
	}
	a.pathGroups[path] = pathGroup{group: group, at: a.Clock.Now()}
	return group
}

type pathGroup struct {
	group string
	at    time.Time
}

// PathOfGroup is a path that lives in the group, to probe its disk with.
func (a *DiskManager) PathOfGroup(group string) (string, bool) {
	for _, row := range a.cachedLayouts() {
		if row.Group == group {
			return row.Path, true
		}
	}
	return "", false
}

func (a *DiskManager) cachedLayouts() []apiserver.DiskPath {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cached == nil || a.Clock.Now().Sub(a.cachedAt) > layoutTTL {
		a.cached, a.cachedAt = a.layouts(), a.Clock.Now()
	}
	return a.cached
}

// Forget drops the cached layout, after a correction.
func (a *DiskManager) Forget() {
	a.mu.Lock()
	a.cached, a.pathGroups = nil, nil
	a.mu.Unlock()
}

// layouts detects every root folder and extra path, with the admin's corrections applied.
func (a *DiskManager) layouts() []apiserver.DiskPath {
	var paths []string
	for _, root := range a.Cache.Snapshot().RootFolders() {
		paths = append(paths, root.Path)
	}
	paths = append(paths, a.ExtraPaths...)
	overrides := a.Cache.Snapshot().DiskGroupOverrides()
	detected := disktopo.Detect(a.FS, paths)
	rows := make([]apiserver.DiskPath, 0, len(detected))
	for _, layout := range detected {
		row := apiserver.DiskPath{
			Path: layout.Path, MountPoint: layout.MountPoint, FSType: layout.FSType, Source: layout.Source,
			Filesystem: layout.Filesystem, Pool: layout.Pool, Disk: layout.Disk, Evidence: layout.Evidence,
			DetectedGroup: layout.Group(), Group: layout.Group(), TotalBytes: layout.TotalBytes, FreeBytes: layout.FreeBytes,
		}
		if label, ok := overrides[layout.Path]; ok {
			row.Group, row.Overridden = label, true
		}
		rows = append(rows, row)
	}
	return rows
}

// Report answers the page.
func (a *DiskManager) Report(ctx context.Context) (apiserver.DiskReport, error) {
	rows := a.layouts()
	report := apiserver.DiskReport{Paths: rows, Latencies: []apiserver.DiskLatency{}}
	seen := map[string]bool{}
	for _, row := range rows {
		seen[row.Group] = true
	}
	measured := map[string]disklatency.Reading{}
	for _, reading := range a.Monitor.Snapshot() {
		measured[reading.Group] = reading
	}
	groups := make([]string, 0, len(seen))
	for group := range seen {
		groups = append(groups, group)
	}
	sort.Strings(groups)
	for _, group := range groups {
		reading := measured[group]
		latency := apiserver.DiskLatency{Group: group, Samples: reading.Samples, Error: reading.Error}
		latency.LastMS, latency.MeanMS = milliseconds(reading.Last), milliseconds(reading.Average)
		if !reading.MeasuredAt.IsZero() {
			latency.MeasuredAtUnix = reading.MeasuredAt.Unix()
		}
		report.Latencies = append(report.Latencies, latency)
	}
	report.Scan.IntervalHours = int64(a.Cache.Snapshot().ScanInterval() / time.Hour)
	if a.ScanRoots != nil {
		report.Scan.Roots = a.ScanRoots(ctx)
	}
	report.Inotify.MaxUserWatches = a.maxWatches()
	report.Inotify.BudgetWatches = report.Inotify.MaxUserWatches / 2
	if a.WatchInUse != nil {
		report.Inotify.UsedByUs, report.Inotify.Directories = a.WatchInUse()
	}
	report.Inotify.RecommendedWatches = watch.RecommendedWatches(int(report.Inotify.Directories))
	return report, nil
}

func milliseconds(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func (a *DiskManager) maxWatches() int64 {
	file, err := a.FS.Open(inotifyLimitFile)
	if err != nil {
		return 0
	}
	defer file.Close() //nolint:errcheck // read-only
	line, _ := bufio.NewReader(file).ReadString('\n')
	value, err := strconv.ParseInt(strings.TrimSpace(line), 10, 64)
	if err != nil {
		return 0
	}
	return value
}

// SetScanInterval stores the hours between scans.
func (a *DiskManager) SetScanInterval(ctx context.Context, hours int64) error {
	return a.Cache.SetScanIntervalHours(ctx, a.Clock, hours)
}

// SetOverrides stores the corrections.
func (a *DiskManager) SetOverrides(ctx context.Context, overrides map[string]string) error {
	defer a.Forget()
	return a.Cache.SetDiskGroupOverrides(ctx, a.Clock, overrides)
}

// WatchBudget is how many inotify watches Solarr may hold: half of the host's limit, 0 when that is unreadable.
func (a *DiskManager) WatchBudget() int { return int(a.maxWatches() / 2) }

// WatchSeesChanges is whether inotify on this path reports every change: not on a network share or a FUSE mount,
// where another machine or process changes files without the kernel here hearing of it.
func (a *DiskManager) WatchSeesChanges(path string) bool {
	for _, row := range a.cachedLayouts() {
		if row.Path == path {
			return !strings.HasPrefix(row.Pool, "net:") && !strings.HasPrefix(row.FSType, "fuse") && row.FSType != "" && !strings.HasPrefix(row.FSType, "nfs") && !strings.HasPrefix(row.FSType, "cifs")
		}
	}
	return false
}

// Measure probes the first path of every group once.
func (a *DiskManager) Measure(_ context.Context) error {
	probed := map[string]bool{}
	for _, row := range a.layouts() {
		if probed[row.Group] {
			continue
		}
		probed[row.Group] = true
		a.Monitor.ProbeNow(row.Group, row.Path)
	}
	return nil
}
