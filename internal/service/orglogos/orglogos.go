// Package orglogos finds the logos of organisations (publishers, studios, networks, labels) by name, for every media type.
package orglogos

import (
	"context"
	"log/slog"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// JobType finds the logos of organisations no provider id can ask for.
const JobType = jobs.Type("organisations.logos_by_name")

// Definition runs in the background; each organisation is asked once, found or not.
var Definition = jobs.Definition{
	Name: "Finding organisation logos",
	Type: JobType, Pool: jobs.PoolMetadata, Mutating: true, MaxAttempts: 2,
}

const (
	perRun = 1000
	batch  = 100
)

// LabelProvider is music.ProviderLabel: the id its own job asks music labels by, so this job leaves those labels alone.
const LabelProvider = "musicbrainz:label"

// Kinds are the organisation kinds whose logos are shown: books' publishers, studios, networks and music labels.
var Kinds = []string{"publisher", "studio", "network", "label"}

// Source answers the logo a provider holds for each organisation name it has one for.
type Source interface {
	OrganisationLogos(ctx context.Context, names []string) (map[string]string, error)
}

// Job asks the source for the logos of the organisations never asked about.
type Job struct {
	Organisations library.Organisations
	Source        Source
	// SkipProvider names organisations another job asks for by id (music labels with a MusicBrainz id).
	SkipProvider string
	Clock        clock.Clock
	Logger       *slog.Logger
}

// Run implements jobs.Handler.
func (j Job) Run(ctx context.Context, _ jobs.Job) error {
	asked, found := 0, 0
	for after := int64(0); asked < perRun; {
		page, err := j.Organisations.LogosToAskByName(ctx, Kinds, j.SkipProvider, after, batch)
		if err != nil || len(page) == 0 {
			return err
		}
		names := make([]string, len(page))
		ids := make([]int64, len(page))
		for i, one := range page {
			names[i], ids[i] = one.Name, one.OrganisationID
		}
		answers, err := j.Source.OrganisationLogos(ctx, names)
		if err != nil {
			return err
		}
		logos := map[int64]string{}
		for _, one := range page {
			if logo := answers[one.Name]; logo != "" {
				logos[one.OrganisationID] = logo
			}
		}
		if err := j.Organisations.AnswerLogos(ctx, j.Clock.Now(), ids, logos); err != nil {
			return err
		}
		asked, found, after = asked+len(page), found+len(logos), page[len(page)-1].OrganisationID
		j.Logger.InfoContext(ctx, "organisation logos asked", slog.Int("asked", asked), slog.Int("found", found))
	}
	return nil
}

// Enqueue submits one run; a second while one waits is the same work.
func Enqueue(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{Type: JobType, ActionKey: string(JobType), Priority: jobs.PriorityLow})
}
