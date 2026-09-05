package main

import (
	"github.com/makearr/MediaNexus/internal/clock"
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
// executor. No enqueue trigger yet -- a move job is created by whichever
// caller accepts a mover.Plan (the still-unbuilt move/rebalance/drain API),
// not a periodic sweep, the same ready-ahead-of-its-trigger shape
// registerImportJob's own comment already explains.
func (p *process) registerMoverJob(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(moverjob.Definition); err != nil {
		return err
	}
	job := moverjob.NewJob(moverjob.Config{
		FS: p.fsys, MoveJobs: library.NewMoveJobs(p.library), Physical: library.NewPhysical(p.library),
		Roots: moverRoots{roots: cacheRoots{cache: p.cache}}, Clock: clock.NewSystem(), Logger: p.logger,
	})
	executor.Handle(moverjob.JobType, job)
	return nil
}
