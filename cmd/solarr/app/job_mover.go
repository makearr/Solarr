package app

import (
	"context"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/moving/drainjob"
	"github.com/makearr/Solarr/internal/moving/mover"
	"github.com/makearr/Solarr/internal/moving/moverjob"
	"github.com/makearr/Solarr/internal/moving/renamejob"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// registerMoverJob wires ADR-51's move.apply job into the registry and the
// executor. The trigger is whichever caller accepts a plan and persists a
// move_job -- adapters.Drain, for now. DrainCompletion closes the
// drain lifecycle: once a DRAIN job's own files have all moved, this checks
// whether the source root is now empty and advances drain_state to
// DRAINED.
func (p *process) registerMoverJob(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(moverjob.Definition); err != nil {
		return err
	}
	job := moverjob.NewJob(moverjob.Config{
		FS: p.fsys, MoveJobs: library.NewMoveJobs(p.library), Physical: library.NewPhysical(p.library), Links: library.NewObjectLinks(p.library),
		Roots: adapters.MoverRoots{Roots: adapters.Roots{Cache: p.cache}},
		DrainCompletion: drainjob.Completer{
			Roots: adapters.DrainRoots{Cache: p.cache}, Files: library.NewPhysical(p.library),
		},
		Permissions: p.libraryPermissions, Clock: clock.NewSystem(), Logger: p.logger,
	})
	executor.Handle(moverjob.JobType, job)
	if err := registry.Register(renamejob.AutomaticDefinition); err != nil {
		return err
	}
	renamer := adapters.Rename{Registry: p.media, Settings: p.cache, Engine: p.renameEngine()}
	executor.Handle(renamejob.AutomaticJobType, renamejob.NewAutomaticJob(renamejob.AutomaticConfig{
		Libraries: p.automaticRenameLibraries,
		Rename: func(ctx context.Context, categoryID int64) error {
			_, err := renamer.StartLibraryRename(ctx, categoryID, 0, false)
			return err
		},
		MoveJobs: library.NewMoveJobs(p.library), Logger: p.logger,
	}))
	return nil
}

// automaticRenameLibraries are the libraries whose naming renames existing files without being asked.
func (p *process) automaticRenameLibraries() []int64 {
	var ids []int64
	for _, category := range p.cache.Snapshot().Categories() {
		if category.Naming != nil && category.Naming.RenameMode() == settings.RenameExistingAutomatic {
			ids = append(ids, category.ID)
		}
	}
	return ids
}

// automaticRenameTask is the registered sweep that renames libraries set to rename automatically.
func (p *process) automaticRenameTask() jobs.Task {
	return jobs.Task{
		Key: renamejob.AutomaticScheduleKey, Name: "Rename libraries set to rename automatically", DefaultCadence: automaticRenameScheduleCron,
		Tier: string(settings.TierAdvanced), Category: jobs.CategoryLibrary,
		Enqueue: func(ctx context.Context) (jobs.EnqueueResult, error) { return renamejob.EnqueueAutomatic(ctx, p.queue) },
	}
}

// libraryPermissions is a library's Set Permissions for the move engine; ok is false when it is off.
func (p *process) libraryPermissions(categoryID int64) (mover.Permissions, bool, error) {
	category, found := p.cache.Snapshot().Category(categoryID)
	if !found || !category.FileManagement.SetPermissions {
		return mover.Permissions{}, false, nil
	}
	management := category.FileManagement
	permissions, err := mover.PermissionsOf(management.FolderMode(), management.FileMode(), management.ChownGroup, lookupGroupID)
	return permissions, err == nil, err
}

// renameEngine is the one rename every library uses; its media type only names the files.
func (p *process) renameEngine() renamejob.Engine {
	return renamejob.Engine{
		Config: renamejob.Config{
			Roots: adapters.DrainRoots{Cache: p.cache}, MoveJobs: library.NewMoveJobs(p.library), Queue: p.queue, Clock: clock.NewSystem(),
		},
		Files: library.NewPhysical(p.library), FS: p.fsys,
	}
}
