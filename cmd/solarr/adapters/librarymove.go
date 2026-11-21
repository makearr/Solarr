package adapters

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/moving/placement"
	"github.com/makearr/Solarr/internal/moving/renamejob"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// LibraryMove satisfies apiserver.LibraryMover: a work goes to another library of its media type through the one
// rename engine, then its items follow (plan 10-libraries L3).
type LibraryMove struct {
	Registry *media.Registry
	Settings *settings.Cache
	Engine   renamejob.Engine
	Works    library.Works
	Desired  library.Desired
	// MoveJobs cancels the file move just queued when the library change that must follow it fails.
	MoveJobs library.MoveJobs
	Clock    clock.Clock
	// Selection resolves a bulk selection to work ids.
	Selection WorksMonitor
	// DefaultProfileID is the profile items take in a library that names none.
	DefaultProfileID int64
	// OnMoved is told when a work changed libraries, for the libraries' counts; nil tells nobody.
	OnMoved func(ctx context.Context)
}

// MoveWorkToLibrary plans, and unless preview starts, one work's move or copy.
func (a LibraryMove) MoveWorkToLibrary(ctx context.Context, request apiserver.LibraryMoveRequest) (apiserver.DrainPlanResult, string, error) {
	return a.moveWork(ctx, request, nil)
}

// moveWork is MoveWorkToLibrary with the bytes earlier works of the same request will occupy on each root, nil for none.
func (a LibraryMove) moveWork(ctx context.Context, request apiserver.LibraryMoveRequest, planned map[int64]int64) (apiserver.DrainPlanResult, string, error) {
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
	dest, found := a.destinationRoot(snapshot, target, detail.RootFolderIDs, workBytes(detail), planned)
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
		// The files are on their way to the other library while the work still belongs to this one: stop the move.
		if jobID != 0 {
			_, _ = a.MoveJobs.Cancel(ctx, jobID, a.Clock.Now().Unix())
		}
		return apiserver.DrainPlanResult{}, "", err
	}
	if !request.Copy && jobID != 0 {
		change, err := json.Marshal(libraryChange{WorkID: request.WorkID, FromCategoryID: source.ID, ToCategoryID: target.ID})
		if err == nil {
			err = a.MoveJobs.SetLibraryChange(ctx, jobID, string(change))
		}
		if err != nil {
			return apiserver.DrainPlanResult{}, "", err
		}
	}
	if planned != nil && jobID != 0 {
		planned[dest] += result.BytesTotal
	}
	if a.OnMoved != nil {
		a.OnMoved(ctx)
	}
	result.Accepted = true
	return result, "", nil
}

// libraryChange is the library switch a move's files follow.
type libraryChange struct {
	WorkID         int64 `json:"work_id"`
	FromCategoryID int64 `json:"from_category_id"`
	ToCategoryID   int64 `json:"to_category_id"`
}

// UndoLibraryChange puts a work back in the library a move job was to take it from, once the job moved none of its files.
func (a LibraryMove) UndoLibraryChange(ctx context.Context, moveJobID int64) error {
	stored, err := a.MoveJobs.LibraryChange(ctx, moveJobID)
	if err != nil || stored == "" {
		return err
	}
	var change libraryChange
	if err := json.Unmarshal([]byte(stored), &change); err != nil {
		return fmt.Errorf("library change of move job %d: %w", moveJobID, err)
	}
	profileID := a.DefaultProfileID
	if origin, found := a.Settings.Snapshot().Category(change.FromCategoryID); found && origin.QualityProfileID != nil {
		profileID = *origin.QualityProfileID
	}
	if _, err := a.Desired.MoveWorkToLibrary(ctx, change.WorkID, change.ToCategoryID, change.FromCategoryID, profileID); err != nil {
		return err
	}
	if a.OnMoved != nil {
		a.OnMoved(ctx)
	}
	return a.MoveJobs.ForgetLibraryChange(ctx, moveJobID)
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

// workBytes is what a work's files on disk add up to, so a root without the room is not chosen.
func workBytes(detail library.WorkDetail) int64 {
	var total int64
	for _, file := range detail.Files {
		total += file.SizeBytes
	}
	for _, part := range detail.Parts {
		if part.File != nil {
			total += part.File.SizeBytes
		}
	}
	return total
}

// destinationRoot is the target root the work's files go to, chosen by the library's placement rules from the roots taking
// new files, preferring the filesystem the work is on (an instant move). planned is what earlier works of the same request
// will already occupy on each root.
func (a LibraryMove) destinationRoot(snapshot *settings.Snapshot, target settings.Category, workRoots []int64, sizeBytes int64, planned map[int64]int64) (int64, bool) {
	filesystem := ""
	for _, id := range workRoots {
		if root, found := snapshot.RootFolder(id); found && root.FSID != "" {
			filesystem = root.FSID
			break
		}
	}
	accepting := map[int64]bool{}
	for _, attachment := range snapshot.CategoryRoots() {
		if attachment.CategoryID == target.ID && attachment.AcceptsNew {
			accepting[attachment.RootFolderID] = true
		}
	}
	var candidates []placement.RootCandidate
	for _, root := range snapshot.RootFoldersForCategory(target.ID) {
		candidate := placement.CandidateOf(root, accepting[root.ID])
		candidate.FreeBytes = max(candidate.FreeBytes-planned[root.ID], 0)
		candidates = append(candidates, candidate)
	}
	chosen, decision, err := placement.Resolve(placement.Request{
		CategoryID: target.ID, SizeBytes: sizeBytes, Strategy: placement.StrategyMostFreeSpace, SourceFSID: filesystem,
	}, candidates)
	if err != nil || decision.Outcome != placement.OutcomePlaced {
		return 0, false
	}
	return chosen.RootFolderID, true
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
	planned := map[int64]int64{}
	for _, id := range ids {
		one := request
		one.WorkID = id
		plan, refusal, err := a.moveWork(ctx, one, planned)
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
