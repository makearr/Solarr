package app

import (
	"context"
	"log/slog"
	"path/filepath"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/metadata/lrclib"
	"github.com/makearr/Solarr/internal/moving/sidecar"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// tagWritebackJob is the tag writer: ids, metadata and covers into library files (revision D3).
func (p *process) tagWritebackJob() media.JobRegistration {
	return media.JobRegistration{Definition: music.TagWritebackDefinition, Handler: music.NewTagWritebackJob(music.TagWritebackConfig{
		Files: library.NewTagWriteback(p.library), Roots: p.tagRoots, Tool: mediatool.New(nil, 0), FS: p.fsys,
		ImagesDir: p.config.Paths.MediaCoverDir,
		CoversDir: filepath.Join(p.config.Paths.CacheDir, "tag-covers"),
		Clock:     clock.NewSystem(), Logger: p.logger,
	})}
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
					WriteMetadata: tagging.WriteMetadata, EmbedCover: tagging.EmbedCover, Navidrome: tagging.Navidrome, CopyLinkedFiles: tagging.CopyLinkedFiles,
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

// lyricsJob writes LRCLIB's lyrics beside the tracks of the music libraries that switched them on.
func (p *process) lyricsJob() media.JobRegistration {
	return media.JobRegistration{Definition: music.LyricsDefinition, Handler: music.LyricsJob{
		Files: library.NewLyrics(p.library), Roots: p.lyricsRoots, Source: lrclib.New(p.providerClient(providerLRCLIB, nil, httpx.ClientConfig{})),
		FS: p.fsys, Clock: clock.NewSystem(), Logger: p.logger,
	}}
}

// lyricsRoots are the roots of music libraries with lyrics on that Solarr may write to: online, probed writable, not read only.
func (p *process) lyricsRoots(context.Context) ([]music.LyricsRoot, error) {
	snapshot := p.cache.Snapshot()
	var roots []music.LyricsRoot
	for _, root := range snapshot.RootFolders() {
		if !sidecar.WritableRoot(root) {
			continue
		}
		for _, categoryID := range snapshot.CategoriesForRootFolder(root.ID) {
			category, found := snapshot.Category(categoryID)
			if found && category.MediaType == string(music.MediaType) &&
				category.FileManagement.ModuleOption(string(media.FileLyrics), music.LyricsOff) == music.LyricsOn {
				roots = append(roots, music.LyricsRoot{ID: root.ID, Path: root.Path})
				break
			}
		}
	}
	return roots, nil
}

func (p *process) enqueueLyrics(ctx context.Context) (jobs.EnqueueResult, error) {
	return music.EnqueueLyrics(ctx, p.queue)
}
