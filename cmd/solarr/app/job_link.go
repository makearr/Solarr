package app

import (
	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/moving/linkjob"
)

// linkConfig is the linking of identical copies, for the preview and the job alike.
func (p *process) linkConfig() linkjob.Config {
	return linkjob.Config{
		FS: p.fsys, Links: library.NewObjectLinks(p.library), Roots: adapters.LinkRoots(p.cache), Logger: p.logger,
	}
}

func (p *process) registerLinkJob(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(linkjob.Definition); err != nil {
		return err
	}
	executor.Handle(linkjob.JobType, linkjob.NewJob(p.linkConfig()))
	return nil
}
