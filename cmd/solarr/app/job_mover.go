package app

import (
	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/moving/drainjob"
	"github.com/makearr/Solarr/internal/moving/moverjob"
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
		FS: p.fsys, MoveJobs: library.NewMoveJobs(p.library), Physical: library.NewPhysical(p.library),
		Roots: adapters.MoverRoots{Roots: adapters.Roots{Cache: p.cache}},
		DrainCompletion: drainjob.Completer{
			Roots: adapters.DrainRoots{Cache: p.cache}, Files: library.NewPhysical(p.library),
		},
		Clock: clock.NewSystem(), Logger: p.logger,
	})
	executor.Handle(moverjob.JobType, job)
	return nil
}
