package app

import (
	"github.com/makearr/MediaNexus/cmd/medianexus/adapters"
	"github.com/makearr/MediaNexus/internal/db/library"
	"github.com/makearr/MediaNexus/internal/importing/importjob"
	"github.com/makearr/MediaNexus/internal/jobs"
	"github.com/makearr/MediaNexus/internal/platform/clock"
	"github.com/makearr/MediaNexus/internal/platform/mediatool"
)

// registerImportJob wires ADR-48's import.inspect job into the registry and
// the executor. There is no enqueue trigger yet -- import.inspect is
// user-triggered (point it at a path), not a periodic sweep like a scan or a
// probe, so the natural trigger is the still-unbuilt POST /api/v1/import/
// inspect endpoint (next.md's own next item), not a startup call. Registered
// now anyway, the same ready-ahead-of-consumer shape every engine piece this
// session landed already has -- the executor has to know the type exists
// before anything can enqueue one.
func (p *process) registerImportJob(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(importjob.Definition); err != nil {
		return err
	}
	job := importjob.NewJob(importjob.Config{
		FS:       p.fsys,
		Sessions: library.NewImportSessions(p.library),
		Policy:   adapters.ImportPolicy{Cache: p.cache},
		Tool:     mediatool.New(nil, 0),
		Events:   p.bus,
		Clock:    clock.NewSystem(),
		Logger:   p.logger,
	})
	executor.Handle(importjob.JobType, job)
	return nil
}
