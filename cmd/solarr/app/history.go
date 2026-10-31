package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/statistics"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/events"
)

// recordDownloadHistory appends one download's grab, import or failure to the durable history
// and tells the status bar. It runs after the state change it records, so a failure is logged only.
func (p *process) recordDownloadHistory(
	ctx context.Context, kind download.HistoryKind, acquisition library.Acquisition, detail string,
) {
	data, workID := p.describeAcquisition(ctx, acquisition)
	data.Detail = detail
	if kind == download.HistoryGrabbed && data.Upgrade && detail == "" {
		data.Detail = "upgrade"
	}

	_, err := statistics.NewHistory(p.statistics).Append(ctx, historyEntry(clock.NewSystem().Now().Unix(), string(kind), workID, data))
	if err != nil {
		p.logger.Warn("download history not recorded", slog.String("kind", string(kind)),
			slog.Int64("acquisition_id", acquisition.ID), slog.Any("error", err))
	}
	p.bus.Publish(events.Event{
		Topic: events.TopicAcquisition, Entity: fmt.Sprintf("work:%d", workID), CategoryID: data.CategoryID,
		Kind: string(kind), Data: data,
	})
}

// historyEntry is one download history row from what was known of the download then.
func historyEntry(at int64, kind string, workID int64, data download.HistoryData) statistics.HistoryEntry {
	return statistics.HistoryEntry{
		At: at, Kind: kind, CategoryID: data.CategoryID, WorkID: workID, AcquisitionID: data.AcquisitionID,
		ReleaseTitle: data.ReleaseTitle, WorkTitle: data.Album, LeadName: data.Artist, Indexer: data.Indexer,
		Client: data.Client, Detail: data.Detail,
	}
}

// historyCopyBatch bounds each transaction of the copy (Invariant 2).
const historyCopyBatch = 200

// copyDownloadHistory moves the download history jobs.db's event log held before statistics.db existed into it,
// once: it resumes after the last event copied, and an event already copied is never copied twice.
func (p *process) copyDownloadHistory(ctx context.Context) error {
	kinds := make([]string, len(download.HistoryKinds))
	for i, kind := range download.HistoryKinds {
		kinds[i] = string(kind)
	}
	history, events := statistics.NewHistory(p.statistics), jobs.NewHistory(p.jobs, clock.NewSystem())
	after, err := history.LastCopiedEvent(ctx)
	if err != nil {
		return err
	}
	copied := 0
	for {
		batch, err := events.ListAfter(ctx, kinds, after, historyCopyBatch)
		if err != nil || len(batch) == 0 {
			if copied > 0 {
				p.logger.Info("download history moved to statistics.db", slog.Int("events", copied))
			}
			return err
		}
		entries := make([]statistics.HistoryEntry, len(batch))
		for i, event := range batch {
			var data download.HistoryData
			// An unreadable payload still says what happened and when.
			_ = json.Unmarshal([]byte(event.Data), &data)
			var workID int64
			if event.SubjectType == "work" {
				workID = event.SubjectID
			}
			entries[i] = historyEntry(event.At, event.Kind, workID, data)
			entries[i].EventID = event.ID
		}
		if err := history.Copy(ctx, entries); err != nil {
			return err
		}
		after, copied = batch[len(batch)-1].ID, copied+len(batch)
	}
}

// describeAcquisition names what a download is for: its album, artist and library, and the indexer
// and client by name. Best-effort: whatever cannot be read is left empty.
func (p *process) describeAcquisition(ctx context.Context, acquisition library.Acquisition) (download.HistoryData, int64) {
	data := download.HistoryData{AcquisitionID: acquisition.ID, ReleaseTitle: acquisition.CandidateTitle}
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
			data.Upgrade = replacesFiles(item.RequiredCount, item.PresentCount)
			if work, err := library.NewWorks(p.library).Detail(ctx, workID); err == nil {
				data.Album = work.Work.CanonicalTitle
				key := media.Key(work.Work.MediaType)
				if labelling, ok := media.Capability[media.Labelling](p.media, key); ok && item.MediaRef != "" {
					data.Album += " " + labelling.ReleaseLabel(acquisition.CandidateTitle, item.MediaRef, clock.NewSystem().Now())
				}
				data.Artist = leadName(p.media, key, work.Credits)
			}
		}
	}
	return data, workID
}

// replacesFiles is true when an item already holds all it requires, so a download for it can only upgrade.
func replacesFiles(required sql.NullInt64, present int64) bool {
	return required.Valid && required.Int64 > 0 && present >= required.Int64
}

// leadName is who a work is listed under, for a media type that has a lead role (an album's artist); empty otherwise.
func leadName(registry *media.Registry, key media.Key, credits []library.Credit) string {
	module, found := registry.Module(key)
	if !found {
		return ""
	}
	lead, has := module.Descriptor().LeadRole()
	if !has {
		return ""
	}
	for _, credit := range credits {
		if credit.Role == lead {
			return credit.PersonName
		}
	}
	return ""
}
