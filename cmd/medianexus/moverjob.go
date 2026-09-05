package main

import (
	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/drainjob"
	"github.com/makearr/MediaNexus/internal/jobs"
	"github.com/makearr/MediaNexus/internal/library"
	"github.com/makearr/MediaNexus/internal/moverjob"
)

// moverRoots adapts the existing cacheRoots (scan.go) to moverjob.RootFolders
// -- same settings snapshot, just the path and filesystem id Apply needs
// rather than scan's own policy fields.
type moverRoots struct{ roots cacheRoots }

func (m moverRoots) RootFolder(id int64) (moverjob.RootFolder, bool) {
	root, ok := m.roots.RootFolder(id)
	if !ok {
		return moverjob.RootFolder{}, false
	}
	return moverjob.RootFolder{Path: root.Path, FSID: root.FSID}, true
}

// registerMoverJob wires ADR-51's move.apply job into the registry and the
// executor. The trigger is whichever caller accepts a plan and persists a
// move_job -- drainAdapter (moves.go), for now. DrainCompletion closes the
// drain lifecycle: once a DRAIN job's own files have all moved, this checks
// whether the source root is now empty and advances drain_state to
// DRAINED.
func (p *process) registerMoverJob(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(moverjob.Definition); err != nil {
		return err
	}
	job := moverjob.NewJob(moverjob.Config{
		FS: p.fsys, MoveJobs: library.NewMoveJobs(p.library), Physical: library.NewPhysical(p.library),
		Roots: moverRoots{roots: cacheRoots{cache: p.cache}},
		DrainCompletion: drainjob.Completer{
			Roots: drainRoots{cache: p.cache}, Files: library.NewPhysical(p.library),
		},
		Clock: clock.NewSystem(), Logger: p.logger,
	})
	executor.Handle(moverjob.JobType, job)
	return nil
}
