package adapters

import (
	"context"
	"fmt"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
)

// defectRetain is how long a recycled defective file stays restorable.
const defectRetain = 7 * 24 * time.Hour

// FileDefects satisfies apiserver.FileDefects over the library's defect table and the one recycle engine.
type FileDefects struct {
	Defects  library.FileDefects
	Desired  library.Desired
	Recycler RemoveAndBlock
}

func (f FileDefects) ListDefects(ctx context.Context, rootFolderIDs []int64, afterID int64, limit int) ([]apiserver.DefectRow, error) {
	rows, err := f.Defects.List(ctx, rootFolderIDs, afterID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]apiserver.DefectRow, len(rows))
	for i, row := range rows {
		out[i] = apiserver.DefectRow{
			MediaObjectID: row.MediaObjectID, RootFolderID: row.RootFolderID, WorkID: row.WorkID, SizeBytes: row.SizeBytes, DetectedAt: row.DetectedAt,
			RelPath: row.RelPath, WorkTitle: row.WorkTitle, Reason: row.Reason, Detail: row.Detail,
		}
	}
	return out, nil
}

func (f FileDefects) CountDefects(ctx context.Context, rootFolderIDs []int64) (int64, error) {
	return f.Defects.Count(ctx, rootFolderIDs)
}

// ResolveDefects recycles each named file that is a defective file of the roots and reopens the items it satisfied, so
// they are wanted again; a file outside the roots is left alone and reported.
func (f FileDefects) ResolveDefects(ctx context.Context, rootFolderIDs, mediaObjectIDs []int64) (apiserver.DefectOutcome, error) {
	var outcome apiserver.DefectOutcome
	inLibrary, err := f.Defects.InRoots(ctx, mediaObjectIDs, rootFolderIDs)
	if err != nil {
		return outcome, err
	}
	allowed := map[int64]bool{}
	for _, id := range inLibrary {
		allowed[id] = true
	}
	seenWorks := map[int64]bool{}
	for _, id := range mediaObjectIDs {
		if err := ctx.Err(); err != nil {
			return outcome, err
		}
		if !allowed[id] {
			outcome.Problems = append(outcome.Problems, fmt.Sprintf("file %d is not a defective file of this library", id))
			continue
		}
		membership, err := f.Desired.ItemsForFile(ctx, id)
		if err != nil {
			outcome.Problems = append(outcome.Problems, fmt.Sprintf("file %d: %v", id, err))
			continue
		}
		var works []int64
		for _, one := range membership {
			item, err := f.Desired.Get(ctx, one.DesiredItemID)
			if err != nil {
				outcome.Problems = append(outcome.Problems, fmt.Sprintf("file %d: %v", id, err))
				continue
			}
			works = append(works, item.WorkID)
		}
		if _, err := f.Recycler.RemoveAndBlockFile(ctx, id, apiserver.RemoveAndBlockInput{
			RecycleBin: true, RecycleReason: string(library.ReasonUserDelete), RecycleRetainHours: int(defectRetain / time.Hour), Reopen: true,
		}); err != nil {
			outcome.Problems = append(outcome.Problems, fmt.Sprintf("file %d: %v", id, err))
			continue
		}
		outcome.Resolved++
		for _, work := range works {
			if !seenWorks[work] {
				seenWorks[work] = true
				outcome.WorkIDs = append(outcome.WorkIDs, work)
			}
		}
	}
	return outcome, nil
}

// Ignores satisfies apiserver.IgnoreStore over the table Discover's "not interested" already used, so there is one ignore concept.
type Ignores struct{ Store library.Recommendations }

func (i Ignores) Ignore(ctx context.Context, userID int64, mediaType, kind, provider, externalID, title string, at time.Time) error {
	return i.Store.Dismiss(ctx, library.RecommendationDismissal{
		UserID: userID, MediaType: mediaType, Kind: kind, Provider: provider, ExternalID: externalID, Title: title, CreatedAt: at.Unix(),
	})
}

func (i Ignores) IgnoreHeld(ctx context.Context, userID int64, person bool, id int64, mediaType string, ignored bool, at time.Time) error {
	return i.Store.IgnoreHeld(ctx, userID, person, id, mediaType, ignored, at.Unix())
}

func (i Ignores) Unignore(ctx context.Context, userID int64, kind, provider, externalID string) error {
	return i.Store.Restore(ctx, userID, kind, provider, externalID)
}

func (i Ignores) IgnoredKeys(ctx context.Context, userID int64, mediaType string) (map[string]bool, error) {
	return i.Store.IgnoredKeys(ctx, userID, mediaType)
}
