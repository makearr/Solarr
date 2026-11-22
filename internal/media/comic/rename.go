package comic

import (
	"context"
	"path"
	"strings"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/moving/renamejob"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// RenameConfig is what naming a library's comic files needs; renamejob.Engine does the rest.
type RenameConfig struct {
	Reader library.SeriesReader
	Clock  clock.Clock
}

// NameFiles is the type's part of a rename: each file named after the first part it holds, in the series' folder.
func (cfg RenameConfig) NameFiles(ctx context.Context, category settings.Category, workID int64, files []renamejob.File) ([]renamejob.Named, error) {
	detail, err := cfg.Reader.GetOf(ctx, MediaType, category.ID, workID, cfg.Clock.Now())
	if err != nil {
		return nil, err
	}
	names := NamingFor(category.Naming)
	named := make([]renamejob.Named, 0, len(files))
	taken := map[string]bool{}
	for _, file := range files {
		keys := make([]library.PartKey, len(file.MediaRefs))
		for index, ref := range file.MediaRefs {
			keys[index] = library.PartKey{MediaRef: ref}
		}
		if len(keys) == 0 {
			continue
		}
		slot, facts := namingOf(detail, importFile{file: inspect.File{RelPath: file.RelPath}, keys: keys})
		if facts.OriginalFilename == "" || facts.OriginalFilename == "." {
			facts.OriginalFilename = strings.TrimSuffix(path.Base(file.RelPath), path.Ext(file.RelPath))
		}
		rendered, warnings, err := names.RenderFile(slot, facts, file.RelPath)
		if err != nil || taken[rendered] {
			continue
		}
		taken[rendered] = true
		named = append(named, renamejob.Named{MediaObjectID: file.MediaObjectID, RelPath: rendered, Warnings: warnings})
	}
	return named, nil
}
