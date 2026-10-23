package renamejob

import (
	"context"
	"fmt"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/moving/mover"
	"github.com/makearr/Solarr/internal/moving/placement"
)

// LibraryMove is one work going to another library (plan 10-libraries L3): its files on the source library's roots go
// to one root of the target, named by the target's naming, moved or, with Copy, copied as further paths.
type LibraryMove struct {
	WorkID         int64
	Source, Target settings.Category
	// SourceRoots are the source library's roots the work's files may be on; DestRoot the target root they go to.
	SourceRoots []int64
	DestRoot    int64
	Copy        bool
}

// StartLibraryMove plans, and unless preview starts, a work's move or copy through the one move engine (ADR-51): the
// plan, the job started (0 for a preview or nothing to move) and naming's warnings; a shared file is refused.
func (e Engine) StartLibraryMove(ctx context.Context, namer Namer, move LibraryMove, preview bool) (mover.MovePlan, int64, map[int64][]string, error) {
	dest, ok := e.Roots.RootFolder(move.DestRoot)
	if !ok {
		return mover.MovePlan{}, 0, nil, fmt.Errorf("renamejob: root folder %d not found", move.DestRoot)
	}
	// The target's names for files the source library holds: the work is read where it is, named as it will be.
	naming := move.Source
	naming.Naming = move.Target.Naming
	plan := mover.MovePlan{Reason: mover.ReasonCategory}
	roots := map[int64]placement.RootCandidate{move.DestRoot: rootCandidate(move.DestRoot, dest)}
	readOnly, allowRename := map[int64]bool{}, map[int64]bool{}
	var files []mover.FileToMove
	warnings := map[int64][]string{}
	for _, sourceID := range move.SourceRoots {
		source, ok := e.Roots.RootFolder(sourceID)
		if !ok {
			continue
		}
		listed, err := e.Files.RenameFilesOnRoot(ctx, move.Source.ID, sourceID, move.WorkID)
		if err != nil {
			return mover.MovePlan{}, 0, nil, err
		}
		var mine []File
		known := map[string]bool{}
		for _, file := range listed {
			known[file.RelPath] = true
			if len(file.Works) != 1 {
				plan.Refused = append(plan.Refused, mover.RefusedFile{MediaObjectID: file.MediaObjectID, Reason: sharedRefusal})
				continue
			}
			work := file.Works[0]
			mine = append(mine, File{FileOnRoot: file.FileOnRoot, WorkID: work.WorkID, EditionID: work.EditionID, MediaRefs: work.MediaRefs})
		}
		if len(mine) == 0 {
			continue
		}
		named, err := namer.NameFiles(ctx, naming, move.WorkID, mine)
		if err != nil {
			return mover.MovePlan{}, 0, nil, fmt.Errorf("renamejob: name the files of work %d: %w", move.WorkID, err)
		}
		proposals := make([]proposal, len(mine))
		for i, file := range mine {
			proposals[i] = proposal{file: file, named: Named{MediaObjectID: file.MediaObjectID, RelPath: file.RelPath}, crossRoot: sourceID != move.DestRoot}
			for _, name := range named {
				if name.MediaObjectID == file.MediaObjectID && name.RelPath != "" {
					proposals[i].named = name
				}
			}
		}
		roots[sourceID] = rootCandidate(sourceID, source)
		readOnly[sourceID], allowRename[sourceID] = source.ReadOnly && !move.Copy, source.AllowRename
		for _, target := range companionsOf(e.FS, source.Path, known, proposals) {
			files = append(files, mover.FileToMove{
				MediaObjectID: target.MediaObjectID, SourceRootFolderID: sourceID, SourceRelPath: target.CurrentRelPath,
				SizeBytes: target.SizeBytes, AffinityKey: fmt.Sprintf("work:%d", target.WorkID),
				DestRootFolderID: move.DestRoot, DestRelPath: target.ProposedRelPath, Companions: target.Companions,
			})
			for _, warning := range target.Warnings {
				warnings[target.MediaObjectID] = append(warnings[target.MediaObjectID], warning.Message)
			}
		}
	}
	if len(files) == 0 {
		return plan, 0, warnings, nil
	}
	planned, err := mover.Plan(mover.Request{
		Reason: mover.ReasonCategory, Files: files, Roots: roots, SourceReadOnly: readOnly, SourceAllowRename: allowRename,
	})
	if err != nil {
		return mover.MovePlan{}, 0, nil, fmt.Errorf("renamejob: plan the move of work %d: %w", move.WorkID, err)
	}
	planned.Refused = append(planned.Refused, plan.Refused...)
	if preview || len(planned.Refused) > 0 || len(planned.Moves) == 0 {
		return planned, 0, warnings, nil
	}
	targetID := move.Target.ID
	jobID, err := commit(ctx, e.Config, planned, &targetID, move.Copy)
	if err != nil {
		return mover.MovePlan{}, 0, nil, fmt.Errorf("renamejob: work %d: %w", move.WorkID, err)
	}
	return planned, jobID, warnings, nil
}
