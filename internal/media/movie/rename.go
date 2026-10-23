package movie

import (
	"context"
	"fmt"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/parsemovie"
	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/moving/renamejob"
)

// RenameMovies answers a movie's facts and the ids providers know it by. Satisfied by library.SeriesReader.
type RenameMovies interface {
	Basics(ctx context.Context, workID int64) (library.WorkBasics, error)
	ProviderIDs(ctx context.Context, workID int64) (map[string]string, error)
}

// RenameConfig is what naming a movie library's files needs (Radarr's Rename Movies); renamejob.Engine does the rest.
type RenameConfig struct {
	Movies RenameMovies
}

// NameFiles is the movie type's part of a rename: the movie's file under the library's naming, from the movie's facts
// and what the file's own name says of its edition and release.
func (cfg RenameConfig) NameFiles(
	ctx context.Context, category settings.Category, workID int64, files []renamejob.File,
) ([]renamejob.Named, error) {
	basics, err := cfg.Movies.Basics(ctx, workID)
	if err != nil {
		return nil, fmt.Errorf("movie %d: %w", workID, err)
	}
	ids, err := cfg.Movies.ProviderIDs(ctx, workID)
	if err != nil {
		return nil, fmt.Errorf("movie %d: %w", workID, err)
	}
	names := NamingFor(category.Naming)
	named := make([]renamejob.Named, 0, len(files))
	for _, file := range files {
		info, _ := parsemovie.ParsePath(file.RelPath)
		media := inspect.File{RelPath: file.RelPath, SizeBytes: file.SizeBytes}
		rendered, warnings, err := names.RenderMovie(fileNaming(basics, ids, info, &media), file.RelPath)
		if err != nil {
			continue
		}
		named = append(named, renamejob.Named{MediaObjectID: file.MediaObjectID, RelPath: rendered, Warnings: warnings})
	}
	return named, nil
}
