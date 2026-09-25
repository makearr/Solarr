package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/events"
)

// recordDownloadHistory appends one download's grab, import or failure to the durable history
// and tells the status bar. It runs after the state change it records, so a failure is logged only.
func (p *process) recordDownloadHistory(
	ctx context.Context, kind download.HistoryKind, acquisition library.Acquisition, detail string,
) {
	data := download.HistoryData{
		AcquisitionID: acquisition.ID, ReleaseTitle: acquisition.CandidateTitle, Detail: detail,
	}
	if indexers, err := p.cache.Indexers(ctx); err == nil && acquisition.IndexerID != nil {
		for _, indexer := range indexers {
			if indexer.ID == *acquisition.IndexerID {
				data.Indexer = indexer.Name
			}
		}
	}
	if clients, err := p.cache.Downloaders(ctx); err == nil {
		for _, client := range clients {
			if client.ID == acquisition.DownloaderID {
				data.Client = client.Name
			}
		}
	}
	var workID int64
	if acquisition.DesiredItemID != nil {
		if item, err := library.NewDesired(p.library).Get(ctx, *acquisition.DesiredItemID); err == nil {
			workID, data.CategoryID = item.WorkID, item.CategoryID
			if work, err := library.NewWorks(p.library).Detail(ctx, workID); err == nil {
				data.Album = work.Work.CanonicalTitle
				for _, credit := range work.Credits {
					if credit.Role == music.CreditRoleArtist {
						data.Artist = credit.PersonName
						break
					}
				}
			}
		}
	}

	encoded, err := json.Marshal(data)
	if err == nil {
		event := jobs.Event{Kind: string(kind), Data: string(encoded)}
		if workID != 0 {
			event.SubjectType, event.SubjectID = "work", workID
		}
		_, err = jobs.NewHistory(p.jobs, clock.NewSystem()).Append(ctx, event)
	}
	if err != nil {
		p.logger.Warn("download history not recorded", slog.String("kind", string(kind)),
			slog.Int64("acquisition_id", acquisition.ID), slog.Any("error", err))
	}
	p.bus.Publish(events.Event{
		Topic: events.TopicAcquisition, Entity: fmt.Sprintf("work:%d", workID), CategoryID: data.CategoryID,
		Kind: string(kind), Data: data,
	})
}
