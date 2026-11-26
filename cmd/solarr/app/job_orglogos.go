package app

import (
	"context"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/service/orglogos"
)

// orgLogosScheduleCron is daily; the job asks only organisations never asked, 1,000 a run.
const orgLogosScheduleCron = "40 5 * * *"

// registerOrganisationLogoJob wires the by-name logo search every media type's organisations share.
func (p *process) registerOrganisationLogoJob(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(orglogos.Definition); err != nil {
		return err
	}
	executor.Handle(orglogos.JobType, orglogos.Job{
		Organisations: library.NewOrganisations(p.library), Source: p.wikidataFetcher(), SkipProvider: orglogos.LabelProvider,
		Clock: clock.NewSystem(), Logger: p.logger,
	})
	return nil
}

func (p *process) organisationLogosTask() jobs.Task {
	return jobs.Task{
		Key: "organisation_logos", Name: "Find organisation logos", DefaultCadence: orgLogosScheduleCron,
		Tier: string(settings.TierAdvanced), Category: jobs.CategoryMetadata, Startup: jobs.StartupOn,
		Enqueue: func(ctx context.Context) (jobs.EnqueueResult, error) { return orglogos.Enqueue(ctx, p.queue) },
	}
}
