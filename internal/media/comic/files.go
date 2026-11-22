package comic

import (
	"context"
	"fmt"
	"path"
	"slices"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// Files is the type's media.PlacingFiles: a queued file becomes a file of the series a person names, on the parts its
// name or metadata gives; with the series' folder when asked.
type Files struct {
	Resolver     Resolver
	FS           fsx.FS
	Unidentified library.Unidentified
	Physical     library.Physical
	Clock        clock.Clock
	// RootPath is the path of a root folder; false when it is unknown.
	RootPath func(rootFolderID int64) (string, bool)
	// AllowAgain lifts the exclusion of a series a person names on purpose; nil lifts nothing.
	AllowAgain func(ctx context.Context, provider, externalID string) error
}

var _ media.PlacingFiles = Files{}

// PlaceFile implements media.PlacingFiles.
func (f Files) PlaceFile(ctx context.Context, categoryID, mediaObjectID int64, placement media.Placement, wholeFolder bool) (int64, int64, error) {
	if !slices.Contains(allProviders, placement.Provider) {
		return 0, 0, fmt.Errorf("%w: %s %q is not a comic provider id", media.ErrInvalidAdd, placement.Provider, placement.ExternalID)
	}
	if f.AllowAgain != nil {
		if err := f.AllowAgain(ctx, placement.Provider, placement.ExternalID); err != nil {
			return 0, 0, err
		}
	}
	workID := f.Resolver.ByIDs(ctx, categoryID, map[string]string{placement.Provider: placement.ExternalID})
	if workID <= 0 {
		return 0, 0, fmt.Errorf("%w: that series could not be added", media.ErrInvalidAdd)
	}
	entry, err := f.Unidentified.Get(ctx, mediaObjectID)
	if err != nil {
		return 0, 0, err
	}
	files := []int64{mediaObjectID}
	if wholeFolder {
		if files, err = f.Unidentified.InFolder(ctx, entry.RootFolderID, entry.RelPath); err != nil {
			return 0, 0, err
		}
	}
	now := f.Clock.Now()
	manga := isMangaKind(f.Resolver.Service.kind(categoryID))
	var placed int64
	for _, id := range files {
		waiting, err := f.Unidentified.Get(ctx, id)
		if err != nil {
			return workID, placed, err
		}
		facts, _ := Probe(ctx, f.FS, path.Join(f.rootPathOf(waiting.RootFolderID), waiting.RelPath))
		keys := PartKeys(claimsOfPath(waiting.RelPath, facts), manga)
		if len(keys) == 0 {
			continue
		}
		if manga {
			f.Resolver.Service.EnsureParts(ctx, categoryID, workID, keys)
		}
		result, err := f.Resolver.Service.Series.AttachFiles(ctx, now, workID, waiting.RootFolderID, []library.SeriesFileLink{{MediaObjectID: id, Parts: keys}})
		if err != nil {
			return workID, placed, err
		}
		if len(result.Unresolved) > 0 {
			continue
		}
		recordPageFacts(ctx, f.Physical, id, path.Ext(waiting.RelPath), facts, now.Unix())
		if err := f.Unidentified.RecordManual(ctx, id, workID, now.Unix()); err != nil {
			return workID, placed, err
		}
		if err := f.Unidentified.Clear(ctx, []int64{id}); err != nil {
			return workID, placed, err
		}
		placed++
	}
	if placed == 0 {
		return workID, 0, fmt.Errorf("%w: no issue, volume or chapter number could be read from the file", media.ErrInvalidAdd)
	}
	return workID, placed, nil
}

func (f Files) rootPathOf(rootFolderID int64) string {
	root, _ := f.RootPath(rootFolderID)
	return root
}
