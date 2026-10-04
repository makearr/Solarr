package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/acquiring/cutoff"
	"github.com/makearr/Solarr/internal/scanning/probe"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media"
)

// registerCutoffJob wires the cutoff check: one job in which every media type checks its own items.
func (p *process) registerCutoffJob(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(cutoff.Definition); err != nil {
		return err
	}
	executor.Handle(cutoff.JobType, cutoff.NewJob(cutoff.Config{
		Desired: library.NewDesired(p.library), Settings: p.cache, Sources: p.cutoffSources, Logger: p.logger,
	}))
	return nil
}

// cutoffSources lists the media types whose items are checked: the one a library holds, or every type for zero.
func (p *process) cutoffSources(categoryID int64) []cutoff.Source {
	var only media.Key
	if categoryID != 0 {
		category, found := p.cache.Snapshot().Category(categoryID)
		if !found {
			return nil
		}
		only = media.Key(category.MediaType)
	}
	var sources []cutoff.Source
	for _, descriptor := range p.media.Descriptors() {
		if only != "" && descriptor.Key != only {
			continue
		}
		if check, ok := media.Capability[media.Upgrading](p.media, descriptor.Key); ok {
			sources = append(sources, cutoff.Source{Name: string(descriptor.Key), Check: check})
		}
	}
	return sources
}

// recycleSuperseded moves a file an upgrade replaced, or a download that measured no better, to the
// recycle bin under its library's retention.
func (p *process) recycleSuperseded(ctx context.Context, mediaObjectID, categoryID int64, replaced bool) error {
	reason := library.ReasonRejectedWorse
	if replaced {
		reason = library.ReasonReplaced
	}
	recycling := adapters.RecyclingFor(adapters.Categories{Cache: p.cache}, []int64{categoryID})
	return p.fileRemover().RecycleFor(ctx, mediaObjectID, recycling, reason)
}

// afterDownloadImported measures a download's files now and then settles its album: an upgrade
// replaces the files it beats instead of waiting for the nightly check.
func (p *process) afterDownloadImported(ctx context.Context, acquisition library.Acquisition) {
	if acquisition.DesiredItemID == nil {
		return
	}
	item, err := library.NewDesired(p.library).Get(ctx, *acquisition.DesiredItemID)
	if err != nil {
		p.logger.Warn("after import: album not found", slog.Any("error", err))
		return
	}
	if _, err := probe.Enqueue(ctx, p.queue); err != nil {
		p.logger.Warn("after import: probe not queued", slog.Any("error", err))
	}
	payload, err := json.Marshal(cutoff.Payload{WorkID: item.WorkID, CategoryID: item.CategoryID})
	if err != nil {
		return
	}
	if _, err := p.queue.Enqueue(ctx, jobs.Request{
		Type: cutoff.JobType, ActionKey: fmt.Sprintf("%s:%d", cutoff.JobType, item.WorkID),
		Priority: jobs.PriorityLow, Payload: payload,
	}); err != nil {
		p.logger.Warn("after import: cutoff check not queued", slog.Any("error", err))
	}
}

func (p *process) enqueueCutoff(ctx context.Context) (jobs.EnqueueResult, error) {
	return p.queue.Enqueue(ctx, jobs.Request{
		Type: cutoff.JobType, ActionKey: string(cutoff.JobType), Priority: jobs.PriorityLow, Payload: []byte("{}"),
	})
}
