package app

import (
	"context"
	"log/slog"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// registerTagWritebackJob wires revision D3's write-back: MusicBrainz ids into identified files.
func (p *process) registerTagWritebackJob(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(music.TagWritebackDefinition); err != nil {
		return err
	}
	executor.Handle(music.TagWritebackJobType, music.NewTagWritebackJob(music.TagWritebackConfig{
		Files: library.NewTagWriteback(p.library), Roots: p.tagRoots, Tool: mediatool.New(nil, 0),
		Clock: clock.NewSystem(), Logger: p.logger,
	}))
	return nil
}

// tagRoots are the roots write-back may touch: online, writable, not read-only (Invariant 69), in
// a music library that has it on.
func (p *process) tagRoots(context.Context) ([]music.TagRoot, error) {
	snapshot := p.cache.Snapshot()
	var roots []music.TagRoot
	for _, root := range snapshot.RootFolders() {
		if !root.Online || !root.Writable || root.ReadOnly {
			continue
		}
		for _, categoryID := range snapshot.CategoriesForRootFolder(root.ID) {
			category, found := snapshot.Category(categoryID)
			if found && category.MediaType == "music" && category.FileManagement.WriteIDsToTags {
				roots = append(roots, music.TagRoot{ID: root.ID, Path: root.Path})
				break
			}
		}
	}
	return roots, nil
}

func (p *process) enqueueTagWriteback(ctx context.Context) (jobs.EnqueueResult, error) {
	result, err := music.EnqueueTagWriteback(ctx, p.queue)
	if err != nil {
		p.logger.Warn("enqueue tag write-back", slog.Any("error", err))
	}
	return result, err
}
