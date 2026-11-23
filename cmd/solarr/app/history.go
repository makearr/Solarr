package app

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/statistics"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/events"
	"github.com/makearr/Solarr/internal/service/webhook"
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
	if kind == download.HistoryGrabbed {
		p.recordWebhookDownload(ctx, webhook.EventGrabbed, acquisition, detail)
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

// describedItem is what one desired item and its work contribute to every download description of it.
type describedItem struct {
	workID, categoryID int64
	mediaRef           string
	upgrade            bool
	workFound          bool
	title, artist      string
	mediaType          string
}

// describeAcquisition names what a download is for: its album, artist and library, and the indexer
// and client by name. Best-effort: whatever cannot be read is left empty.
func (p *process) describeAcquisition(ctx context.Context, acquisition library.Acquisition) (download.HistoryData, int64) {
	return p.newDescriber(ctx)(acquisition)
}

// newDescriber answers describeAcquisition for many downloads of one read: the indexer and client names are read once
// and an item (several held releases belong to one) is looked up once.
func (p *process) newDescriber(ctx context.Context) func(library.Acquisition) (download.HistoryData, int64) {
	indexerNames, clientNames := map[int64]string{}, map[int64]string{}
	if indexers, err := p.cache.Indexers(ctx); err == nil {
		for _, indexer := range indexers {
			indexerNames[indexer.ID] = indexer.Name
		}
	}
	if clients, err := p.cache.Downloaders(ctx); err == nil {
		for _, client := range clients {
			clientNames[client.ID] = client.Name
		}
	}
	items := map[int64]*describedItem{}
	desired, works := library.NewDesired(p.library), library.NewWorks(p.library)
	describedItemOf := func(itemID int64) *describedItem {
		if found, ok := items[itemID]; ok {
			return found
		}
		var described *describedItem
		if item, err := desired.Get(ctx, itemID); err == nil {
			described = &describedItem{
				workID: item.WorkID, categoryID: item.CategoryID, mediaRef: item.MediaRef, upgrade: replacesFiles(item.RequiredCount, item.PresentCount),
			}
			if work, err := works.Headline(ctx, item.WorkID); err == nil {
				key := media.Key(work.Work.MediaType)
				described.workFound, described.title, described.mediaType = true, work.Work.CanonicalTitle, work.Work.MediaType
				described.artist = leadName(p.media, key, work.Credits)
			}
		}
		items[itemID] = described
		return described
	}
	return func(acquisition library.Acquisition) (download.HistoryData, int64) {
		data := download.HistoryData{AcquisitionID: acquisition.ID, ReleaseTitle: acquisition.CandidateTitle, Client: clientNames[acquisition.DownloaderID]}
		if acquisition.IndexerID != nil {
			data.Indexer = indexerNames[*acquisition.IndexerID]
		}
		if acquisition.DesiredItemID == nil {
			return data, 0
		}
		described := describedItemOf(*acquisition.DesiredItemID)
		if described == nil {
			return data, 0
		}
		data.CategoryID, data.Upgrade = described.categoryID, described.upgrade
		if described.workFound {
			data.Album, data.Artist = described.title, described.artist
			if labelling, ok := media.Capability[media.Labelling](p.media, media.Key(described.mediaType)); ok && described.mediaRef != "" {
				if label := labelling.ReleaseLabel(acquisition.CandidateTitle, described.mediaRef, clock.NewSystem().Now()); label != "" {
					data.Album += " " + label
				}
			}
		}
		return data, described.workID
	}
}

// newWorkLookup answers which work a download is for with one item read per distinct item, and no naming.
func (p *process) newWorkLookup(ctx context.Context) func(library.Acquisition) int64 {
	works := map[int64]int64{}
	desired := library.NewDesired(p.library)
	return func(acquisition library.Acquisition) int64 {
		if acquisition.DesiredItemID == nil {
			return 0
		}
		if workID, ok := works[*acquisition.DesiredItemID]; ok {
			return workID
		}
		var workID int64
		if item, err := desired.Get(ctx, *acquisition.DesiredItemID); err == nil {
			workID = item.WorkID
		}
		works[*acquisition.DesiredItemID] = workID
		return workID
	}
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
