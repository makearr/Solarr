package music

import (
	"context"
	"log/slog"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// LabelLogosJobType finds the labels' logos at Wikidata, by the MusicBrainz ids the labels carry.
const LabelLogosJobType = jobs.Type("music.label_logos")

// LabelLogosDefinition runs in the background; each label is asked once, found or not.
var LabelLogosDefinition = jobs.Definition{
	Name: "Finding label logos",
	Type: LabelLogosJobType, Pool: jobs.PoolMetadata, Mutating: true, MaxAttempts: 2,
}

// labelLogosPerRun bounds the labels one run asks about; the query service answers a batch of them in one request.
const labelLogosPerRun = 1000

// labelLogoBatch is how many labels one Wikidata query names.
const labelLogoBatch = 200

// LabelLogoSource answers the logo Wikidata holds for each MusicBrainz label id it has one for.
type LabelLogoSource interface {
	LabelLogos(ctx context.Context, mbids []string) (map[string]string, error)
}

// LabelLogosJob asks Wikidata for the logos of the labels never asked about.
type LabelLogosJob struct {
	Organisations library.Organisations
	Source        LabelLogoSource
	Clock         clock.Clock
	Logger        *slog.Logger
}

// Run implements jobs.Handler.
func (j LabelLogosJob) Run(ctx context.Context, _ jobs.Job) error {
	asked, found := 0, 0
	for after := int64(0); asked < labelLogosPerRun; {
		batch, err := j.Organisations.LogosToAsk(ctx, RoleLabel, ProviderLabel, after, labelLogoBatch)
		if err != nil || len(batch) == 0 {
			return err
		}
		mbids := make([]string, len(batch))
		ids := make([]int64, len(batch))
		for i, label := range batch {
			mbids[i], ids[i] = label.ExternalID, label.OrganisationID
		}
		answers, err := j.Source.LabelLogos(ctx, mbids)
		if err != nil {
			return err
		}
		logos := map[int64]string{}
		for _, label := range batch {
			if logo := answers[label.ExternalID]; logo != "" {
				logos[label.OrganisationID] = logo
			}
		}
		if err := j.Organisations.AnswerLogos(ctx, j.Clock.Now(), ids, logos); err != nil {
			return err
		}
		asked, found, after = asked+len(batch), found+len(logos), batch[len(batch)-1].OrganisationID
		j.Logger.InfoContext(ctx, "label logos asked", slog.Int("asked", asked), slog.Int("found", found))
	}
	return nil
}

// EnqueueLabelLogos submits one run; a second while one waits is the same work.
func EnqueueLabelLogos(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{Type: LabelLogosJobType, ActionKey: string(LabelLogosJobType), Priority: jobs.PriorityLow})
}
