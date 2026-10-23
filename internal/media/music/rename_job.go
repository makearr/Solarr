package music

import (
	"context"
	"fmt"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/moving/renamejob"
)

// RenameWorkDetails answers a work's full detail. Satisfied directly by library.Works.
type RenameWorkDetails interface {
	Detail(ctx context.Context, workID int64) (library.WorkDetail, error)
}

// RenameConfig is what naming a music library's files needs; renamejob.Engine does the rest of a rename.
type RenameConfig struct {
	Works RenameWorkDetails
}

// NameFiles is music's part of a rename: each of an album's files under the library's naming, from the track it holds.
// A file whose track the album does not list keeps its name.
func (cfg RenameConfig) NameFiles(
	ctx context.Context, category settings.Category, workID int64, files []renamejob.File,
) ([]renamejob.Named, error) {
	detail, err := cfg.Works.Detail(ctx, workID)
	if err != nil {
		return nil, fmt.Errorf("music: work detail for %d: %w", workID, err)
	}
	names := NamingFor(category.Naming)
	named := make([]renamejob.Named, 0, len(files))
	for _, file := range files {
		if len(file.MediaRefs) == 0 {
			continue
		}
		identity := library.FileIdentity{WorkID: workID, MediaRef: file.MediaRefs[0]}
		if file.EditionID != 0 {
			identity.EditionID = &file.EditionID
		}
		target, err := RenameTargetFor(names, detail, identity, file.MediaObjectID, file.SizeBytes, file.RelPath)
		if err != nil {
			continue
		}
		named = append(named, renamejob.Named{MediaObjectID: file.MediaObjectID, RelPath: target.ProposedRelPath, Warnings: target.Warnings})
	}
	return named, nil
}
