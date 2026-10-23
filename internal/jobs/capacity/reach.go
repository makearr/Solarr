package capacity

import (
	"context"
	"log/slog"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// ReachJobType is the registered type of the reachability check.
const ReachJobType = jobs.Type("settings.reach")

// ReachActionKey deduplicates the check, which covers every root at once.
const ReachActionKey = "settings.reach"

// ReachDefinition declares the check: housekeeping, cheap, never retried (the next run is minutes away).
var ReachDefinition = jobs.Definition{
	Type: ReachJobType, Pool: jobs.PoolHousekeeping, Mutating: true, MaxAttempts: 1,
}

// ReachJob takes a root it cannot reach offline and brings back one it took offline once it is reachable again
// (user, 2026-10-23); a root a person declared offline is left alone (settings.Cache.RecordRootFolderReach).
type ReachJob struct {
	fsys       fsx.FS
	cache      *settings.Cache
	holdsFiles func(ctx context.Context, rootFolderID int64) (bool, error)
	logger     *slog.Logger
}

// NewReachJob builds the handler from the capacity sweep's own configuration; Clock is not needed.
func NewReachJob(config Config) *ReachJob {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &ReachJob{fsys: config.FS, cache: config.Cache, holdsFiles: config.HoldsFiles, logger: logger}
}

// Run checks every root once.
func (j *ReachJob) Run(ctx context.Context, _ jobs.Job) error {
	for _, root := range j.cache.Snapshot().RootFolders() {
		if err := ctx.Err(); err != nil {
			return jobs.Retryablef("reach.context", string(ReachJobType), err)
		}
		reached, reason, entries := Reach(j.fsys, root)
		// A root Solarr emptied itself (a move to another library, a drain) is empty, not unmounted.
		if !reached && entries == 0 && reason == emptyReason && j.holdsFiles != nil {
			if holds, err := j.holdsFiles(ctx, root.ID); err == nil && !holds {
				reached, reason = true, ""
			}
		}
		if err := j.cache.RecordRootFolderReach(ctx, root.ID, reached, reason, entries); err != nil {
			j.logger.WarnContext(ctx, "could not record whether a root folder is reachable",
				slog.Int64("root_folder_id", root.ID), slog.Any("error", err))
			continue
		}
		if !reached && root.Online {
			j.logger.WarnContext(ctx, "root folder unreachable, declared offline until it is back",
				slog.Int64("root_folder_id", root.ID), slog.String("path", root.Path), slog.String("reason", reason))
		}
		if reached && !root.Online && root.OfflineAuto {
			j.logger.InfoContext(ctx, "root folder reachable again, declared online",
				slog.Int64("root_folder_id", root.ID), slog.String("path", root.Path))
		}
	}
	return nil
}

// Reach reports whether a root can be reached and how many entries it holds. A directory that is empty although it
// held entries when last reached is an unmounted disk's mount point, not a library.
func Reach(fsys fsx.FS, root settings.RootFolder) (reached bool, reason string, entries int64) {
	info, err := fsys.Stat(root.Path)
	if err != nil {
		return false, err.Error(), 0
	}
	if !info.IsDir {
		return false, "is not a directory", 0
	}
	listed, err := fsys.ReadDir(root.Path)
	if err != nil {
		return false, err.Error(), 0
	}
	if len(listed) == 0 && root.SeenEntries > 0 {
		return false, emptyReason, 0
	}
	return true, "", int64(len(listed))
}

// emptyReason is why a root that held files and is empty now is taken offline.
const emptyReason = "is empty although it held files: is the disk mounted?"

// EnqueueReach submits one reachability check, deduplicated by ReachActionKey.
func EnqueueReach(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{
		Type: ReachJobType, ActionKey: ReachActionKey, Priority: jobs.PriorityLow,
		Payload: []byte("{}"),
	})
}
