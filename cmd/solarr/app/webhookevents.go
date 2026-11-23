package app

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/service/webhook"
)

// recordWebhookOccurrence queues an event of a library; the caller fills what it knows.
func (p *process) recordWebhookOccurrence(ctx context.Context, categoryID int64, occurrence webhook.Occurrence) {
	category, found := p.cache.Snapshot().Category(categoryID)
	if !found {
		return
	}
	occurrence.Library = webhook.Library{ID: category.ID, Name: category.Name, MediaType: category.MediaType, Kind: category.Kind}
	occurrence.Adult = p.categoryAdult(category)
	if err := p.webhookService().Record(ctx, occurrence); err != nil {
		p.logger.Warn("could not queue a webhook", "event", string(occurrence.Event), "error", err)
	}
}

// recordWebhookDownload tells the webhooks a release was grabbed or a download failed, from the history seam.
func (p *process) recordWebhookDownload(ctx context.Context, event webhook.Event, acquisition library.Acquisition, detail string) {
	data, workID := p.describeAcquisition(ctx, acquisition)
	if data.CategoryID == 0 {
		return
	}
	occurrence := webhook.Occurrence{
		Event: event, Message: detail,
		Release: &webhook.Release{Title: data.ReleaseTitle, Indexer: data.Indexer, Protocol: acquisition.Protocol, Size: acquisition.SizeBytes},
	}
	if workID != 0 {
		occurrence.Work = &webhook.Work{ID: workID, Title: data.Album}
	}
	p.recordWebhookOccurrence(ctx, data.CategoryID, occurrence)
}

// recordWebhookFailure tells import_failed for a download whose files the import could not use and download_failed for every other failure.
func (p *process) recordWebhookFailure(ctx context.Context, acquisition library.Acquisition, reason library.BlocklistReason, detail string) {
	event := webhook.EventDownloadFailed
	if reason == library.BlocklistImportFailed {
		event = webhook.EventImportFailed
	}
	p.recordWebhookDownload(ctx, event, acquisition, detail)
}

// webhookWorkAdder tells the webhooks of a work that was added.
type webhookWorkAdder struct {
	apiserver.WorkAdder
	p *process
}

func (a webhookWorkAdder) Add(ctx context.Context, request apiserver.AddWorkRequest) (apiserver.AddWorkResult, error) {
	result, err := a.WorkAdder.Add(ctx, request)
	if err == nil && result.WorkID != 0 {
		a.p.recordWebhook(ctx, request.CategoryID, result.WorkID, webhook.EventWorkAdded)
	}
	return result, err
}

// webhookWorksMonitor tells the webhooks of a work that was removed, with the title read before it is gone.
type webhookWorksMonitor struct {
	apiserver.WorksMonitor
	p *process
}

func (m webhookWorksMonitor) RemoveWork(ctx context.Context, workID int64, deleteFiles, exclude bool) (apiserver.RemoveWorkResult, error) {
	detail, detailErr := library.NewWorks(m.p.library).Detail(ctx, workID)
	result, err := m.WorksMonitor.RemoveWork(ctx, workID, deleteFiles, exclude)
	if err == nil && result.WorkDeleted && detailErr == nil {
		for _, categoryID := range detail.CategoryIDs {
			m.p.recordWebhookOccurrence(ctx, categoryID, webhook.Occurrence{
				Event: webhook.EventWorkRemoved, Work: &webhook.Work{ID: workID, Title: detail.Work.CanonicalTitle},
			})
		}
	}
	return result, err
}

// webhookRequests tells the webhooks of a request filed for a decision and of one approved.
type webhookRequests struct {
	apiserver.Requests
	p *process
}

func (r webhookRequests) File(ctx context.Context, request apiserver.NewRequestRecord) (apiserver.RequestRecord, error) {
	record, err := r.Requests.File(ctx, request)
	if err == nil && record.State == "PENDING" {
		r.p.recordWebhookOccurrence(ctx, record.CategoryID, webhook.Occurrence{Event: webhook.EventRequestCreated, Message: "a request is waiting for a decision"})
	}
	return record, err
}

func (r webhookRequests) Fulfil(ctx context.Context, id, decidedBy, workID int64) (bool, error) {
	changed, err := r.Requests.Fulfil(ctx, id, decidedBy, workID)
	if err == nil && changed {
		if record, getErr := r.Get(ctx, id); getErr == nil {
			r.p.recordWebhookOccurrence(ctx, record.CategoryID, webhook.Occurrence{
				Event: webhook.EventRequestApproved, Work: &webhook.Work{ID: workID}, Message: "a request was approved",
			})
		}
	}
	return changed, err
}

// healthChanges says which checks appeared and which went away between two looks.
func healthChanges(before, now map[string]string) (raised, cleared []string) {
	for id := range now {
		if _, was := before[id]; !was {
			raised = append(raised, id)
		}
	}
	for id := range before {
		if _, is := now[id]; !is {
			cleared = append(cleared, id)
		}
	}
	slices.Sort(raised)
	slices.Sort(cleared)
	return raised, cleared
}

// healthWatch remembers the last set of health checks so a change can be told once; the first look only learns.
type healthWatch struct {
	mutex  sync.Mutex
	last   map[string]string
	lookAt time.Time
}

// healthWatchEvery bounds how often the checks are evaluated for the webhooks.
const healthWatchEvery = 5 * time.Minute

// watchHealth looks at the system health at most every five minutes when a webhook takes health events.
func (p *process) watchHealth(ctx context.Context) {
	hooks, err := p.cache.Webhooks(ctx)
	if err != nil {
		return
	}
	wanted := false
	for _, hook := range hooks {
		wanted = wanted || (hook.Enabled && (slices.Contains(hook.Events, string(webhook.EventHealthRaised)) || slices.Contains(hook.Events, string(webhook.EventHealthCleared))))
	}
	now := clock.NewSystem().Now()
	p.health.mutex.Lock()
	defer p.health.mutex.Unlock()
	if !wanted || now.Sub(p.health.lookAt) < healthWatchEvery {
		return
	}
	p.health.lookAt = now
	current := map[string]string{}
	for _, check := range p.systemHealth().Checks(ctx) {
		current[check.ID] = check.Message
	}
	previous := p.health.last
	p.health.last = current
	if previous == nil {
		return
	}
	raised, cleared := healthChanges(previous, current)
	for _, id := range raised {
		p.recordWebhookInstance(ctx, webhook.EventHealthRaised, current[id])
	}
	for _, id := range cleared {
		p.recordWebhookInstance(ctx, webhook.EventHealthCleared, previous[id])
	}
}

// recordWebhookInstance queues an event that belongs to no library.
func (p *process) recordWebhookInstance(ctx context.Context, event webhook.Event, message string) {
	if err := p.webhookService().Record(ctx, webhook.Occurrence{Event: event, Message: message}); err != nil {
		p.logger.Warn("could not queue a webhook", "event", string(event), "error", err)
	}
}
