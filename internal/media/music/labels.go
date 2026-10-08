package music

import (
	"context"
	"log/slog"
	"strings"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
)

// ProviderLabel is the external-id provider of a MusicBrainz label.
const ProviderLabel = "musicbrainz:label"

// RoleLabel is the role an album's label holds on it, and the organisation kind it is.
const RoleLabel = "label"

// labelAsked is the provider an album's label answer is recorded under, so it is asked once.
const labelAsked = "musicbrainz"

// noLabelMBID is MusicBrainz's own "[no label]": a release naming it has none.
const noLabelMBID = "157afde4-4bf5-4039-8ad2-5a15acc85176"

// labelBackfillPerRun bounds the albums one enrichment run asks about a label for; most answers are cached.
const labelBackfillPerRun = 500

// labelOf is the label a release names, nil for none.
func labelOf(release musicbrainz.Release) *library.OrganisationLink {
	name := strings.TrimSpace(release.LabelName)
	if name == "" || release.LabelMBID == noLabelMBID || strings.EqualFold(name, "[no label]") {
		return nil
	}
	return &library.OrganisationLink{Kind: RoleLabel, Name: name, Provider: ProviderLabel, ExternalID: release.LabelMBID}
}

// linkLabel records the album's label from its release; a label seen for the first time gets its own details.
func (j *EnrichJob) linkLabel(ctx context.Context, workID int64, release musicbrainz.Release) {
	if j.organisations == nil {
		return
	}
	link := labelOf(release)
	id, created, err := j.organisations.SetWorkOrganisation(ctx, j.clock.Now(), workID, RoleLabel, labelAsked, link)
	if err != nil {
		j.logger.WarnContext(ctx, "label link failed", slog.Int64("work_id", workID), slog.Any("error", err))
		return
	}
	if !created || link == nil || link.ExternalID == "" {
		return
	}
	label, err := j.provider.Label(ctx, musicbrainz.PriorityBackfill, link.ExternalID)
	if err != nil {
		j.logger.WarnContext(ctx, "label lookup failed", slog.String("mbid", link.ExternalID), slog.Any("error", err))
		return
	}
	if err := j.organisations.FillDetails(ctx, id, library.OrganisationDetails{
		Country: label.Country, Type: label.Type, Founded: label.Founded, Defunct: label.Defunct,
	}); err != nil {
		j.logger.WarnContext(ctx, "label details failed", slog.Int64("organisation_id", id), slog.Any("error", err))
	}
}

// backfillLabels asks every album not yet asked for its label, from the cached releases mostly.
func (j *EnrichJob) backfillLabels(ctx context.Context, budget int) {
	if j.organisations == nil {
		return
	}
	linked, asked := 0, 0
	var after int64
	for asked < budget && ctx.Err() == nil {
		works, err := j.organisations.WorksToAsk(ctx, string(MediaType), ProviderReleaseGroup, RoleLabel, labelAsked, after, min(100, budget-asked))
		if err != nil {
			j.logger.WarnContext(ctx, "label backfill failed", slog.Any("error", err))
			return
		}
		if len(works) == 0 {
			break
		}
		for _, work := range works {
			after = work.WorkID
			asked++
			if work.EditionLabel == "" {
				continue
			}
			_, release, err := j.provider.Album(ctx, musicbrainz.PriorityBackfill, work.ExternalID, work.EditionLabel)
			if err != nil {
				continue
			}
			j.linkLabel(ctx, work.WorkID, release)
			if labelOf(release) != nil {
				linked++
			}
		}
	}
	if asked > 0 {
		j.logger.Info("album labels backfilled", slog.Int("asked", asked), slog.Int("linked", linked))
	}
}
