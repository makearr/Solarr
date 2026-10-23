package renamejob

import (
	"context"
	"fmt"
	"slices"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/moving/mover"
	"github.com/makearr/Solarr/internal/moving/naming"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// File is one file of a work a rename names: where it is, and the edition and parts of the work it holds.
type File struct {
	library.FileOnRoot
	WorkID    int64
	EditionID int64
	MediaRefs []string
}

// Named is the path a media type's naming gives one file below its root, with naming's notices about it.
type Named struct {
	MediaObjectID int64
	RelPath       string
	Warnings      []naming.Warning
}

// Namer is a media type's whole part of a rename: the path its library's naming gives each of one work's files. A
// file it cannot name is left out and stays where it is.
type Namer interface {
	NameFiles(ctx context.Context, category settings.Category, workID int64, files []File) ([]Named, error)
}

// Files lists what a rename may move; satisfied by library.Physical.
type Files interface {
	RenameFilesOnRoot(ctx context.Context, categoryID, rootFolderID, workID int64) ([]library.RenameFile, error)
}

// Engine is the rename every media type shares (user, 2026-10-23): only the names, one work at a time, are the type's.
type Engine struct {
	Config
	Files Files
	// FS reads the folders the files are in, to find their companions.
	FS fsx.FS
}

// sharedRefusal is why a file two works hold is not renamed: either name would take it from the other.
const sharedRefusal = "held by two works; left where it is"

// Start renames the library's files on one root, one work's when workID is not 0. preview plans without starting.
// It answers the plan, the job started (0 for a preview or nothing to do) and naming's warnings by media object.
func (e Engine) Start(
	ctx context.Context, namer Namer, category settings.Category, rootFolderID, workID int64, preview bool,
) (mover.MovePlan, int64, map[int64][]string, error) {
	listed, err := e.Files.RenameFilesOnRoot(ctx, category.ID, rootFolderID, workID)
	if err != nil {
		return mover.MovePlan{}, 0, nil, err
	}
	var shared []mover.RefusedFile
	byWork := map[int64][]File{}
	var order []int64
	for _, file := range listed {
		if len(file.Works) != 1 {
			shared = append(shared, mover.RefusedFile{MediaObjectID: file.MediaObjectID, Reason: sharedRefusal})
			continue
		}
		work := file.Works[0]
		if _, seen := byWork[work.WorkID]; !seen {
			order = append(order, work.WorkID)
		}
		byWork[work.WorkID] = append(byWork[work.WorkID], File{
			FileOnRoot: file.FileOnRoot, WorkID: work.WorkID, EditionID: work.EditionID, MediaRefs: work.MediaRefs,
		})
	}

	var proposals []proposal
	for _, id := range order {
		files := byWork[id]
		named, err := namer.NameFiles(ctx, category, id, files)
		if err != nil {
			return mover.MovePlan{}, 0, nil, fmt.Errorf("renamejob: name the files of work %d: %w", id, err)
		}
		for _, name := range named {
			index := slices.IndexFunc(files, func(file File) bool { return file.MediaObjectID == name.MediaObjectID })
			if index < 0 || name.RelPath == "" {
				continue
			}
			proposals = append(proposals, proposal{file: files[index], named: name})
		}
	}

	if !slices.ContainsFunc(proposals, proposal.changed) {
		return mover.MovePlan{Reason: mover.ReasonRename, Refused: shared}, 0, nil, nil
	}
	root, ok := e.Roots.RootFolder(rootFolderID)
	if !ok {
		return mover.MovePlan{}, 0, nil, fmt.Errorf("renamejob: root folder %d not found", rootFolderID)
	}
	known := make(map[string]bool, len(listed))
	for _, file := range listed {
		known[file.RelPath] = true
	}
	targets := companionsOf(e.FS, root.Path, known, proposals)
	plan, jobID, warnings, err := Start(ctx, e.Config, rootFolderID, category.ID, targets, preview)
	if err != nil {
		return mover.MovePlan{}, 0, nil, err
	}
	plan.Refused = append(plan.Refused, shared...)
	return plan, jobID, warnings, nil
}

// proposal is one named file: where it is and where its naming puts it.
type proposal struct {
	file  File
	named Named
}

func (p proposal) changed() bool { return p.named.RelPath != p.file.RelPath }
