package adapters

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/moving/renamejob"
)

// LibraryMove satisfies apiserver.LibraryMover: a work goes to another library of its media type through the one
// rename engine, then its items follow (plan 10-libraries L3).
type LibraryMove struct {
	Registry *media.Registry
	Settings *settings.Cache
	Engine   renamejob.Engine
	Works    library.Works
	Desired  library.Desired
	// Selection resolves a bulk selection to work ids.
	Selection WorksMonitor
	// DefaultProfileID is the profile items take in a library that names none.
	DefaultProfileID int64
	// OnMoved is told when a work changed libraries, for the libraries' counts; nil tells nobody.
	OnMoved func(ctx context.Context)
}

// MoveWorkToLibrary plans, and unless preview starts, one work's move or copy.
func (a LibraryMove) MoveWorkToLibrary(ctx context.Context, request apiserver.LibraryMoveRequest) (apiserver.DrainPlanResult, string, error) {
	snapshot := a.Settings.Snapshot()
	target, found := snapshot.Category(request.ToCategoryID)
	if !found {
		return apiserver.DrainPlanResult{}, "", fmt.Errorf("%w: category %d", settings.ErrNotFound, request.ToCategoryID)
	}
	detail, err := a.Works.Detail(ctx, request.WorkID)
	if errors.Is(err, sql.ErrNoRows) {
		return apiserver.DrainPlanResult{}, "", fmt.Errorf("%w: work %d", settings.ErrNotFound, request.WorkID)
	}
	if err != nil {
		return apiserver.DrainPlanResult{}, "", err
	}
	source, refusal := a.sourceLibrary(snapshot, detail, target, request.FromCategoryID)
	if refusal != "" {
		return apiserver.DrainPlanResult{}, refusal, nil
	}
	if !target.AdmitsAge(detail.RatingAge) {
		return apiserver.DrainPlanResult{}, fmt.Sprintf("%s's maximum rating does not admit it", target.Name), nil
	}
	namer, ok := media.Capability[media.Renaming](a.Registry, media.Key(target.MediaType))
	if !ok {
		return apiserver.DrainPlanResult{}, fmt.Sprintf("a %s library cannot name its files", target.MediaType), nil
	}
	sourceRoots := rootIDs(snapshot.RootFoldersForCategory(source.ID))
	dest, found := a.destinationRoot(snapshot, target, detail.RootFolderIDs)
	if !found {
		return apiserver.DrainPlanResult{}, fmt.Sprintf("%s has no root folder that takes new files", target.Name), nil
	}
	plan, jobID, warnings, err := a.Engine.StartLibraryMove(ctx, namer, renamejob.LibraryMove{
		WorkID: request.WorkID, Source: source, Target: target, SourceRoots: sourceRoots, DestRoot: dest, Copy: request.Copy,
	}, request.Preview)
	if err != nil {
		return apiserver.DrainPlanResult{}, "", err
	}
	result := movePlanResult(plan, jobID != 0, jobID)
	for i, move := range result.Moves {
		result.Moves[i].Warnings = warnings[move.MediaObjectID]
	}
	if request.Preview || len(plan.Refused) > 0 {
		return result, "", nil
	}
	profileID := a.DefaultProfileID
	if target.QualityProfileID != nil {
		profileID = *target.QualityProfileID
	}
	if request.Copy {
		_, err = a.Desired.CopyWorkToLibrary(ctx, request.WorkID, source.ID, target.ID, profileID)
	} else {
		_, err = a.Desired.MoveWorkToLibrary(ctx, request.WorkID, source.ID, target.ID, profileID)
	}
	if err != nil {
		return apiserver.DrainPlanResult{}, "", err
	}
	if a.OnMoved != nil {
		a.OnMoved(ctx)
	}
	result.Accepted = true
	return result, "", nil
}

// sourceLibrary is the library the work leaves: the one named, else its first other library of the target's type.
func (a LibraryMove) sourceLibrary(
	snapshot *settings.Snapshot, detail library.WorkDetail, target settings.Category, fromID int64,
) (settings.Category, string) {
	if detail.Work.MediaType != target.MediaType {
		return settings.Category{}, fmt.Sprintf("%s holds %s, not %s", target.Name, target.MediaType, detail.Work.MediaType)
	}
	// Moving onto its own copy would move the file onto itself; the copy is removed from one library instead.
	if slices.Contains(detail.CategoryIDs, target.ID) {
		return settings.Category{}, fmt.Sprintf("it is in %s already", target.Name)
	}
	for _, categoryID := range detail.CategoryIDs {
		if fromID != 0 && categoryID != fromID {
			continue
		}
		if source, found := snapshot.Category(categoryID); found {
			return source, ""
		}
	}
	return settings.Category{}, "it is in no library to leave"
}

// destinationRoot is the target root the work's files go to: one taking new files, online, writable and not read-only,
// on the filesystem the work is on when one is (an instant move), else with the most room.
func (a LibraryMove) destinationRoot(snapshot *settings.Snapshot, target settings.Category, workRoots []int64) (int64, bool) {
	filesystems := map[string]bool{}
	for _, id := range workRoots {
		if root, found := snapshot.RootFolder(id); found && root.FSID != "" {
			filesystems[root.FSID] = true
		}
	}
	accepting := map[int64]bool{}
	for _, attachment := range snapshot.CategoryRoots() {
		if attachment.CategoryID == target.ID && attachment.AcceptsNew {
			accepting[attachment.RootFolderID] = true
		}
	}
	var usable []settings.RootFolder
	for _, root := range snapshot.RootFoldersForCategory(target.ID) {
		if accepting[root.ID] && root.Online && root.Writable && !root.ReadOnly {
			usable = append(usable, root)
		}
	}
	if len(usable) == 0 {
		return 0, false
	}
	slices.SortStableFunc(usable, func(x, y settings.RootFolder) int {
		if same := boolRank(filesystems[y.FSID]) - boolRank(filesystems[x.FSID]); same != 0 {
			return same
		}
		return cmp.Compare(freeBytes(y), freeBytes(x))
	})
	return usable[0].ID, true
}

// MoveSelectionToLibrary moves or copies every work of a selection, each on its own: a work that cannot go is
// counted among the refused and the rest go on.
func (a LibraryMove) MoveSelectionToLibrary(
	ctx context.Context, selection apiserver.BulkSelection, request apiserver.LibraryMoveRequest,
) (apiserver.BulkLibraryMoveResult, error) {
	ids, err := a.Selection.matchingWorkIDs(ctx, selection)
	if err != nil {
		return apiserver.BulkLibraryMoveResult{}, err
	}
	var result apiserver.BulkLibraryMoveResult
	for _, id := range ids {
		one := request
		one.WorkID = id
		plan, refusal, err := a.MoveWorkToLibrary(ctx, one)
		if err != nil {
			return result, err
		}
		if refusal == "" && len(plan.Refused) > 0 {
			refusal = plan.Refused[0].Reason
		}
		if refusal != "" {
			title := ""
			if detail, err := a.Works.Detail(ctx, id); err == nil {
				title = detail.Work.CanonicalTitle
			}
			result.Refused = append(result.Refused, apiserver.LibraryMoveRefusal{WorkID: id, Title: title, Reason: refusal})
			continue
		}
		result.Works++
		result.Files += plan.FilesTotal
		result.Bytes += plan.BytesTotal
		if plan.Accepted {
			result.Started++
		}
	}
	return result, nil
}

func rootIDs(roots []settings.RootFolder) []int64 {
	ids := make([]int64, len(roots))
	for i, root := range roots {
		ids[i] = root.ID
	}
	return ids
}

func freeBytes(root settings.RootFolder) int64 {
	if root.FreeBytes == nil {
		return 0
	}
	return *root.FreeBytes
}

func boolRank(value bool) int {
	if value {
		return 1
	}
	return 0
}
