// Package capacity is section 11.7's polling job: it refreshes writable/total_bytes/free_bytes per root on an interval so Placement.Resolve has a recent reading.
package capacity

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// JobType is the registered type of the capacity sweep.
const JobType = jobs.Type("settings.capacity")

// ActionKey deduplicates the sweep: global, not per-root, since one run
// refreshes every configured root (Invariant 16).
const ActionKey = "settings.capacity"

// Definition declares how the sweep behaves: background, low priority,
// retried on transient failure.
var Definition = jobs.Definition{
	Type: JobType, Pool: jobs.PoolHousekeeping, Mutating: true, MaxAttempts: 3,
}

// probeFileName duplicates apiserver's write-probe convention (FilesystemRoots.probeWritable) because this job must not depend on apiserver, and the two must agree on writability.
const probeFileName = ".mnx-write-probe"

// Job refreshes writable/total_bytes/free_bytes for every configured root
// folder.
type Job struct {
	fsys   fsx.FS
	cache  *settings.Cache
	clock  clock.Clock
	logger *slog.Logger
	idle   func(root settings.RootFolder) bool
	bytes  func(ctx context.Context, rootFolderID int64) (int64, error)
}

// Config is what a Job needs. Every field is required except Logger.
type Config struct {
	FS     fsx.FS
	Cache  *settings.Cache
	Clock  clock.Clock
	Logger *slog.Logger
	// HoldsFiles says the library still has files on a root; nil takes any root once seen full and now empty offline.
	HoldsFiles func(ctx context.Context, rootFolderID int64) (bool, error)
	// Idle says nothing has used the root's disk lately and the root has a reading already, so touching the disk
	// would only keep it spinning; nil never skips.
	Idle func(root settings.RootFolder) bool
	// LibraryBytes is what the library holds on a root; nil leaves the figure at zero.
	LibraryBytes func(ctx context.Context, rootFolderID int64) (int64, error)
}

// NewJob builds the handler.
func NewJob(config Config) *Job {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Job{fsys: config.FS, cache: config.Cache, clock: config.Clock, logger: logger, idle: config.Idle, bytes: config.LibraryBytes}
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
		if !root.Online && root.OfflineAuto {
			// Unreachable: an unmounted mount point would report its parent filesystem's space.
			continue
		}
		if j.idle != nil && j.idle(root) {
			continue
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

	// Empty when unreadable: an unknown filesystem must never look like a shared one.
	fsID, _ := j.fsys.FilesystemID(root.Path)
	var held int64
	if j.bytes != nil {
		if held, err = j.bytes(ctx, root.ID); err != nil {
			j.logger.WarnContext(ctx, "library bytes of a root unreadable", slog.Int64("root_folder_id", root.ID), slog.Any("error", err))
		}
	}
	return j.cache.SetRootFolderCapacity(ctx, root.ID, settings.RootFolderCapacity{
		Writable: writable, WritableReason: reason,
		TotalBytes: totalBytes, FreeBytes: freeBytes,
		CheckedAt: j.clock.Now().Unix(), FSID: fsID, LibraryBytes: held,
	})
}

// probeWritable is FilesystemRoots.probeWritable's own create-then-remove
// check, duplicated rather than imported (see probeFileName's own comment).
func (j *Job) probeWritable(directory string) (writable bool, reason string) {
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		return false, fmt.Sprintf("create write probe name: %v", err)
	}
	probe := filepath.Join(directory, probeFileName+"-"+hex.EncodeToString(suffix))
	file, err := j.fsys.Create(probe)
	if err != nil {
		return false, err.Error()
	}
	if err := file.Close(); err != nil {
		return false, errors.Join(err, j.fsys.Remove(probe)).Error()
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
