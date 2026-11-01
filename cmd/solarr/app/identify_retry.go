package app

import (
	"context"
	"log/slog"
	"slices"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/scanning/scan"
)

// identifyRetryDirectoriesPerScan bounds one rescan job, so a large queue becomes several short jobs.
const identifyRetryDirectoriesPerScan = 200

// identifyRetryTask is "Identify waiting files": the files a scan could not place are asked about again, because a scan
// only offers new or changed files and a provider outage or a fix would otherwise leave them waiting for good.
func (p *process) identifyRetryTask() jobs.Task {
	return jobs.Task{
		Key: media.IdentifyRetryKey, Name: "Identify waiting files", DefaultCadence: identifyRetryScheduleCron,
		Tier: string(settings.TierNormal), Category: jobs.CategoryLibrary, Enqueue: p.enqueueIdentifyRetry,
	}
}

// enqueueIdentifyRetry rescans the folders of the waiting files of every library whose type declares this retry (a type
// with its own, TV's, is left to it); each folder goes through its own media type's consumer, probed as a scan would.
func (p *process) enqueueIdentifyRetry(ctx context.Context) (jobs.EnqueueResult, error) {
	unidentified := library.NewUnidentified(p.library)
	snapshot := p.cache.Snapshot()
	var last jobs.EnqueueResult
	for _, category := range snapshot.Categories() {
		if !sharedRetry(p.media, category.MediaType) {
			continue
		}
		for _, root := range snapshot.RootFoldersForCategory(category.ID) {
			directories, err := adapters.WaitingDirectories(ctx, unidentified, root.ID)
			if err != nil {
				return last, err
			}
			for chunk := range slices.Chunk(directories, identifyRetryDirectoriesPerScan) {
				result, err := scan.EnqueueDirectories(ctx, p.queue, root.ID, category.ID, chunk)
				if err != nil {
					p.logger.Warn("enqueue identify retry", slog.Int64("root_folder_id", root.ID), slog.Any("error", err))
					return last, err
				}
				last = result
			}
		}
	}
	return last, nil
}

// sharedRetry says a library of the media type has its waiting files retried by this schedule, as its type declares.
func sharedRetry(registry *media.Registry, mediaType string) bool {
	module, found := registry.Module(media.Key(mediaType))
	return found && module.Descriptor().UnidentifiedRetry == media.IdentifyRetryKey
}
