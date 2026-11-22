package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/artwork"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

const (
	coverShardJobType jobs.Type = "covers.shard"
	// coverShardKey is the periodic action that moves library pictures an older layout left in one folder into their shard folders.
	coverShardKey = "shard_covers"
	// coverShardScheduleCron runs weekly; after the first pass there is nothing left to move.
	coverShardScheduleCron = "40 4 * * 0"
)

var coverShardDefinition = jobs.Definition{Name: "Sorting library pictures into folders", Type: coverShardJobType, Pool: jobs.PoolMaintenance, Mutating: true, MaxAttempts: 1}

type coverShardJob struct {
	fsys   fsx.FS
	dir    string
	logger *slog.Logger
}

// Run moves one chunk of pictures at a time until none lies directly in the folder; readers find either place meanwhile.
func (j coverShardJob) Run(ctx context.Context, _ jobs.Job) error {
	total := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		moved, err := artwork.ShardCovers(j.fsys, j.dir)
		total += moved
		if err != nil {
			return fmt.Errorf("covers: shard after %d files: %w", total, err)
		}
		if moved == 0 {
			break
		}
	}
	if total > 0 {
		j.logger.Info("library covers moved into shard folders", slog.Int("files", total))
	}
	return nil
}

// registerCoverJobs wires the move of library pictures into shard folders.
func (p *process) registerCoverJobs(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(coverShardDefinition); err != nil {
		return err
	}
	executor.Handle(coverShardJobType, coverShardJob{fsys: p.fsys, dir: p.config.Paths.MediaCoverDir, logger: p.logger})
	return nil
}

// coverShardTask is the registered "Sort library pictures into folders" action; it also runs at start-up.
func (p *process) coverShardTask() jobs.Task {
	return jobs.Task{
		Key: coverShardKey, Name: "Sort library pictures into folders", DefaultCadence: coverShardScheduleCron,
		Tier: string(settings.TierAdvanced), Category: jobs.CategoryMaintenance, Startup: jobs.StartupOn,
		Enqueue: func(ctx context.Context) (jobs.EnqueueResult, error) {
			return p.queue.Enqueue(ctx, jobs.Request{
				Type: coverShardJobType, ActionKey: string(coverShardJobType), Priority: jobs.PriorityLow, Payload: []byte("{}"),
			})
		},
	}
}
