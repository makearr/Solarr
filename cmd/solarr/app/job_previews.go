package app

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

const previewCleanupJobType = jobs.Type("library.preview_cleanup")

var previewCleanupDefinition = jobs.Definition{
	Type: previewCleanupJobType, Name: "Removing previews of deleted files", Pool: jobs.PoolHousekeeping, Mutating: true, MaxAttempts: 3,
}

// existingObjects says which media objects still exist; library.Physical is the real one.
type existingObjects interface {
	ExistingObjectIDs(ctx context.Context, ids []int64) (map[int64]bool, error)
}

// previewCleanupJob deletes the preview pictures of files that no longer exist, whichever way they went.
type previewCleanupJob struct {
	fs       fsx.FS
	dir      string
	physical existingObjects
	logger   *slog.Logger
}

func (j previewCleanupJob) Run(ctx context.Context, _ jobs.Job) error {
	folders, err := j.fs.ReadDir(j.dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	var removed int
	for _, folder := range folders {
		if !folder.IsDir {
			continue
		}
		path := filepath.Join(j.dir, folder.Name)
		files, err := j.fs.ReadDir(path)
		if err != nil {
			return err
		}
		owners := map[string][]string{}
		var ids []int64
		for _, file := range files {
			id, _, ok := strings.Cut(file.Name, "-")
			number, err := strconv.ParseInt(id, 10, 64)
			if !ok || err != nil {
				continue
			}
			if len(owners[id]) == 0 {
				ids = append(ids, number)
			}
			owners[id] = append(owners[id], file.Name)
		}
		existing, err := j.physical.ExistingObjectIDs(ctx, ids)
		if err != nil {
			return err
		}
		for _, number := range ids {
			if existing[number] {
				continue
			}
			for _, name := range owners[strconv.FormatInt(number, 10)] {
				if err := j.fs.Remove(filepath.Join(path, name)); err == nil {
					removed++
				}
			}
		}
		// An emptied folder goes too; Remove refuses a folder that still holds a picture.
		_ = j.fs.Remove(path)
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	if removed > 0 {
		j.logger.Info("previews of deleted files removed", slog.Int("pictures", removed))
	}
	return nil
}

func (p *process) enqueuePreviewCleanup(ctx context.Context) (jobs.EnqueueResult, error) {
	return p.enqueueMaintenanceJob(ctx, previewCleanupJobType)
}
