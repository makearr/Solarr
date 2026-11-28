package app

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/statistics"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/measure"
	"github.com/makearr/Solarr/internal/platform/procstat"
)

// libraryFiguresEvery spaces the per-library counts: they change slowly, so a row per minute would only be noise.
const libraryFiguresEvery = 10

// runCounts counts finished jobs by type between two minute ticks.
type runCounts struct {
	mu     sync.Mutex
	counts map[string]int
}

func (r *runCounts) add(jobType string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.counts == nil {
		r.counts = map[string]int{}
	}
	r.counts[jobType]++
}

func (r *runCounts) take() map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	taken := r.counts
	r.counts = nil
	return taken
}

// startMetrics writes one row per measured figure every minute: the process's own CPU, memory and IO, how its disk
// and network calls answered, and how many scans and metadata jobs finished.
func (p *process) startMetrics(ctx context.Context) {
	go func() {
		wall := clock.NewSystem()
		sampler := procstat.New(p.fsys, wall)
		sampler.Rates()
		ticker := wall.NewTicker(time.Minute)
		defer ticker.Stop()
		var ticks int
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C():
				now := wall.Now()
				samples := p.minuteSamples(sampler)
				if ticks%libraryFiguresEvery == 0 {
					samples = append(samples, p.libraryFigures(ctx)...)
				}
				ticks++
				if err := p.metrics.Record(ctx, now.Add(-time.Minute), samples); err != nil {
					p.logger.Warn("write the measured figures", slog.Any("error", err))
				}
				if ticks%60 == 1 {
					if err := p.metrics.Maintain(ctx, now); err != nil {
						p.logger.Warn("roll up the measured figures", slog.Any("error", err))
					}
				}
			}
		}
	}()
}

func (p *process) minuteSamples(sampler *procstat.Sampler) []statistics.MetricSample {
	var samples []statistics.MetricSample
	add := func(series, scope string, value float64) {
		samples = append(samples, statistics.MetricSample{Series: series, Scope: scope, Value: value})
	}
	if rates, ok := sampler.Rates(); ok {
		add("process.cpu_percent", "", rates.CPUPercent)
		add("process.resident_bytes", "", float64(rates.ResidentBytes))
		add("process.read_bytes_per_sec", "", rates.ReadBytesPerSec)
		add("process.write_bytes_per_sec", "", rates.WriteBytesPerSec)
		add("process.disk_read_bytes_per_sec", "", rates.DiskReadBytesPerSec)
		add("process.disk_write_bytes_per_sec", "", rates.DiskWriteBytesPerSec)
	}
	var netBytes, netLatency float64
	var netProviders int
	groups := map[string]*diskGroupFigures{}
	for _, stat := range p.timings.rec.Snapshot() {
		switch stat.Kind {
		case measure.Disk:
			group := p.disks.GroupOfPath(stat.Key)
			if groups[group] == nil {
				groups[group] = &diskGroupFigures{}
			}
			groups[group].add(stat)
		case measure.Net:
			add("net.requests_per_min", stat.Key, stat.OpsPerMin)
			add("net.latency_ms", stat.Key, float64(stat.Recent.Microseconds())/1000)
			add("net.bytes_per_sec", stat.Key, stat.BytesPerSec)
			netBytes += stat.BytesPerSec
			if stat.Recent > 0 {
				netLatency += float64(stat.Recent.Microseconds()) / 1000
				netProviders++
			}
		}
	}
	watches := map[string]int{}
	if p.watch != nil {
		for root, count := range p.watch.Status().ByRoot {
			if group, _, found := p.disks.GroupOfRoot(root); found {
				watches[group] += count
			}
		}
	}
	for group, figures := range groups {
		add("diskgroup.calls_per_min", group, figures.calls)
		add("diskgroup.latency_ms", group, figures.meanLatencyMs())
		add("diskgroup.bytes_per_sec", group, figures.bytesPerSec)
		add("diskgroup.degradation", group, figures.degradation)
	}
	for group, count := range watches {
		add("diskgroup.watches", group, float64(count))
	}
	add("net.bytes_per_sec", "", netBytes)
	if netProviders > 0 {
		add("net.latency_ms", "", netLatency/float64(netProviders))
	}
	scans, metadata := 0, 0
	for jobType, count := range p.runs.take() {
		definition, found := p.jobRegistry.Lookup(jobs.Type(jobType))
		if !found {
			continue
		}
		switch definition.Pool {
		case jobs.PoolScan, jobs.PoolRescan:
			scans += count
		case jobs.PoolMetadata:
			metadata += count
		}
	}
	// The download clients' own speed, summed and per client; one failing client is left out of the minute.
	if job := p.pollJob(); job != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		_, statuses, err := job.Queue(ctx)
		cancel()
		if err == nil {
			var total float64
			for _, status := range statuses {
				if status.Err != nil || status.Status.Paused {
					continue
				}
				add("downloads.bytes_per_sec", status.Name, float64(status.Status.SpeedBytesPerSec))
				total += float64(status.Status.SpeedBytesPerSec)
			}
			add("downloads.bytes_per_sec", "", total)
		}
	}
	add("jobs.scans_per_min", "", float64(scans))
	add("jobs.metadata_per_min", "", float64(metadata))
	return samples
}

// diskGroupFigures is what Solarr's disk calls on every folder of one disk group add up to in a minute.
type diskGroupFigures struct {
	calls, bytesPerSec, degradation float64
	latencyMs                       float64
	timed                           int
}

func (g *diskGroupFigures) add(stat measure.Stat) {
	g.calls += stat.OpsPerMin
	g.bytesPerSec += stat.BytesPerSec
	g.degradation = max(g.degradation, stat.Degradation)
	if stat.Recent > 0 {
		g.latencyMs += float64(stat.Recent.Microseconds()) / 1000
		g.timed++
	}
}

func (g *diskGroupFigures) meanLatencyMs() float64 {
	if g.timed == 0 {
		return 0
	}
	return g.latencyMs / float64(g.timed)
}

// libraryFigures are each library's item and file counts and the bytes on its folders.
func (p *process) libraryFigures(ctx context.Context) []statistics.MetricSample {
	snapshot := p.cache.Snapshot()
	bytesByRoot := map[int64]int64{}
	for _, root := range snapshot.RootFolders() {
		bytesByRoot[root.ID] = root.LibraryBytes
	}
	counters := library.NewCounters(p.library)
	var samples []statistics.MetricSample
	for _, category := range snapshot.Categories() {
		scope := strconv.FormatInt(category.ID, 10)
		values, err := counters.Counters(ctx, "category:"+scope)
		if err != nil {
			continue
		}
		var bytes int64
		for _, attachment := range snapshot.CategoryRoots() {
			if attachment.CategoryID == category.ID {
				bytes += bytesByRoot[attachment.RootFolderID]
			}
		}
		samples = append(samples,
			statistics.MetricSample{Series: "library.items", Scope: scope, Value: float64(values["works"])},
			statistics.MetricSample{Series: "library.files", Scope: scope, Value: float64(values["files"])},
			statistics.MetricSample{Series: "library.bytes", Scope: scope, Value: float64(bytes)},
		)
	}
	return samples
}
