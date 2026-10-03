package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/scanning/probe"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// registerCutoffJob wires M1d's cutoff check: albums whose files fall short of the cutoff become UPGRADABLE.
func (p *process) registerCutoffJob(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(music.CutoffDefinition); err != nil {
		return err
	}
	if err := registry.Register(tv.CutoffDefinition); err != nil {
		return err
	}
	executor.Handle(tv.CutoffJobType, tv.CutoffJob{
		Desired: library.NewDesired(p.library), Series: library.NewSeriesReader(p.library), Settings: p.cache,
		Recycle: p.recycleSuperseded, Logger: p.logger,
	})
	executor.Handle(music.CutoffJobType, music.CutoffJob{
		Desired: library.NewDesired(p.library), Settings: p.cache, Recycle: p.recycleSuperseded,
		Clock: clock.NewSystem(), Logger: p.logger,
	})
	return nil
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
	jobType := music.CutoffJobType
	var payload []byte
	if library.NewSeriesReader(p.library).InCategory(ctx, item.CategoryID, item.WorkID) == nil {
		jobType = tv.CutoffJobType
		payload, err = json.Marshal(tv.CutoffPayload{WorkID: item.WorkID})
	} else {
		payload, err = json.Marshal(music.CutoffPayload{WorkID: item.WorkID})
	}
	if err != nil {
		return
	}
	if _, err := p.queue.Enqueue(ctx, jobs.Request{
		Type: jobType, ActionKey: fmt.Sprintf("%s:%d", jobType, item.WorkID),
		Priority: jobs.PriorityLow, Payload: payload,
	}); err != nil {
		p.logger.Warn("after import: cutoff check not queued", slog.Any("error", err))
	}
}

func (p *process) enqueueCutoff(ctx context.Context) (jobs.EnqueueResult, error) {
	if _, err := p.queue.Enqueue(ctx, jobs.Request{
		Type: tv.CutoffJobType, ActionKey: string(tv.CutoffJobType), Priority: jobs.PriorityLow, Payload: []byte("{}"),
	}); err != nil {
		return jobs.EnqueueResult{}, err
	}
	return p.queue.Enqueue(ctx, jobs.Request{
		Type: music.CutoffJobType, ActionKey: string(music.CutoffJobType), Priority: jobs.PriorityLow, Payload: []byte("{}"),
	})
}
