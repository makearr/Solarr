package app

import (
	"context"
	"log/slog"
	"path/filepath"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// registerTagWritebackJob wires the tag writer: ids, metadata and covers into library files (revision D3).
func (p *process) registerTagWritebackJob(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(music.TagWritebackDefinition); err != nil {
		return err
	}
	executor.Handle(music.TagWritebackJobType, music.NewTagWritebackJob(music.TagWritebackConfig{
		Files: library.NewTagWriteback(p.library), Roots: p.tagRoots, Tool: mediatool.New(nil, 0), FS: p.fsys,
		ImagesDir: filepath.Join(p.config.Paths.CacheDir, "images"),
		CoversDir: filepath.Join(p.config.Paths.CacheDir, "tag-covers"),
		Clock:     clock.NewSystem(), Logger: p.logger,
	}))
	return nil
}

// tagRoots are the roots the tag writer may touch: a music library's own roots, online, writable and
// not read-only (Invariant 69). Download folders are not root folders, so they are never written.
func (p *process) tagRoots(context.Context) ([]music.TagRoot, error) {
	snapshot := p.cache.Snapshot()
	var roots []music.TagRoot
	for _, root := range snapshot.RootFolders() {
		if !root.Online || !root.Writable || root.ReadOnly {
			continue
		}
		for _, categoryID := range snapshot.CategoriesForRootFolder(root.ID) {
			category, found := snapshot.Category(categoryID)
			if found && category.MediaType == string(music.MediaType) {
				tagging := category.FileManagement.Tagging
				roots = append(roots, music.TagRoot{ID: root.ID, Path: root.Path, Options: music.TagOptions{
					WriteIDs: tagging.WriteIDs, ReplaceIDs: tagging.ReplaceIDs,
					WriteMetadata: tagging.WriteMetadata, EmbedCover: tagging.EmbedCover, Navidrome: tagging.Navidrome,
				}})
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
