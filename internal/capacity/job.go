// Package capacity is section 11.7's polling job: "capacity is polled, not
// assumed". It refreshes writable/total_bytes/free_bytes for every
// configured root folder on an interval, so Placement.Resolve (a pure
// function with no filesystem access of its own) always has a recent
// reading to filter candidates against.
package capacity

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/fsx"
	"github.com/makearr/MediaNexus/internal/jobs"
	"github.com/makearr/MediaNexus/internal/settings"
)

// JobType is the registered type of the capacity sweep.
const JobType = jobs.Type("settings.capacity")

// ActionKey deduplicates the sweep: global, not per-root, since one run
// refreshes every configured root (Invariant 16).
const ActionKey = "settings.capacity"

// Definition declares how the sweep behaves: background, low priority,
// retried on transient failure.
var Definition = jobs.Definition{
	Type: JobType, Pool: jobs.PoolMaintenance, Mutating: true, MaxAttempts: 3,
}

// probeFileName mirrors internal/apiserver's own write-probe convention
// (FilesystemRoots.probeWritable) -- duplicated rather than imported, since
// apiserver sits above internal packages in the dependency graph and this
// job must not depend on it. Kept identical so the two never disagree about
// whether a root is writable.
const probeFileName = ".mnx-write-probe"

// Job refreshes writable/total_bytes/free_bytes for every configured root
// folder.
type Job struct {
	fsys   fsx.FS
	cache  *settings.Cache
	clock  clock.Clock
	logger *slog.Logger
}

// Config is what a Job needs. Every field is required except Logger.
type Config struct {
	FS     fsx.FS
	Cache  *settings.Cache
	Clock  clock.Clock
	Logger *slog.Logger
}

// NewJob builds the handler.
func NewJob(config Config) *Job {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Job{fsys: config.FS, cache: config.Cache, clock: config.Clock, logger: logger}
}

// Run refreshes every configured root folder once. One root failing (an
// unmounted disk, a permission error) is logged and does not stop the rest
// -- the next scheduled run tries again.
func (j *Job) Run(ctx context.Context, _ jobs.Job) error {
	roots := j.cache.Snapshot().RootFolders()
	refreshed, failed := 0, 0
	for _, root := range roots {
		if err := ctx.Err(); err != nil {
			return jobs.Retryablef("capacity.context", string(JobType), err)
		}
		if err := j.refreshOne(ctx, root); err != nil {
			failed++
			j.logger.WarnContext(ctx, "capacity probe failed",
				slog.Int64("root_folder_id", root.ID), slog.String("path", root.Path), slog.Any("error", err))
			continue
		}
		refreshed++
	}
	j.logger.Info("capacity sweep finished", slog.Int("refreshed", refreshed), slog.Int("failed", failed))
	return nil
}

func (j *Job) refreshOne(ctx context.Context, root settings.RootFolder) error {
	writable, reason := j.probeWritable(root.Path)

	totalBytes, freeBytes, err := j.fsys.DiskUsage(root.Path)
	if err != nil {
		// DiskUsage failing (an unmounted root) is itself evidence the root
		// is not writable either, even if the write probe above somehow
		// disagreed -- report it rather than writing a stale/zero capacity.
		writable = false
		if reason == "" {
			reason = err.Error()
		}
		totalBytes, freeBytes = 0, 0
	}

	return j.cache.SetRootFolderCapacity(ctx, root.ID, settings.RootFolderCapacity{
		Writable: writable, WritableReason: reason,
		TotalBytes: totalBytes, FreeBytes: freeBytes,
		CheckedAt: j.clock.Now().Unix(),
	})
}

// probeWritable is FilesystemRoots.probeWritable's own create-then-remove
// check, duplicated rather than imported (see probeFileName's own comment).
func (j *Job) probeWritable(directory string) (writable bool, reason string) {
	probe := filepath.Join(directory, probeFileName)
	file, err := j.fsys.Create(probe)
	if err != nil {
		return false, err.Error()
	}
	if err := file.Close(); err != nil {
		return false, err.Error()
	}
	if err := j.fsys.Remove(probe); err != nil {
		// Wrote successfully but could not clean up: still writable, but
		// worth naming so a stray probe file is not a total mystery later.
		return true, fmt.Sprintf("wrote a probe file but could not remove it: %v", err)
	}
	return true, ""
}

// Enqueue submits one capacity sweep, deduplicated by ActionKey.
func Enqueue(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{
		Type: JobType, ActionKey: ActionKey, Priority: jobs.PriorityLow,
		Payload: []byte("{}"),
	})
}
