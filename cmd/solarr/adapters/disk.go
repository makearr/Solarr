package adapters

import (
	"bufio"
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/disklatency"
	"github.com/makearr/Solarr/internal/platform/disktopo"
	"github.com/makearr/Solarr/internal/platform/fsx"
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
	// WatchesInUse says how many inotify watches Solarr holds; nil means none are taken.
	WatchesInUse func() int64
}

// layouts detects every root folder and extra path, with the admin's corrections applied.
func (a DiskManager) layouts() []apiserver.DiskPath {
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
func (a DiskManager) Report(_ context.Context) (apiserver.DiskReport, error) {
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
	report.Inotify.MaxUserWatches = a.maxWatches()
	report.Inotify.BudgetWatches = report.Inotify.MaxUserWatches / 2
	if a.WatchesInUse != nil {
		report.Inotify.UsedByUs = a.WatchesInUse()
	}
	return report, nil
}

func milliseconds(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func (a DiskManager) maxWatches() int64 {
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

// SetOverrides stores the corrections.
func (a DiskManager) SetOverrides(ctx context.Context, overrides map[string]string) error {
	return a.Cache.SetDiskGroupOverrides(ctx, a.Clock, overrides)
}

// Measure probes the first path of every group once.
func (a DiskManager) Measure(_ context.Context) error {
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
