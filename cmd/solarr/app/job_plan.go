package app

import (
	"context"
	"errors"
	"log/slog"

	"github.com/makearr/Solarr/internal/acquiring/plan"
	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/priority"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// registerPlanJobs wires M1c's planner, hunt and RSS sync: what is missing is found without being asked.
func (p *process) registerPlanJobs(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(plan.Definition); err != nil {
		return err
	}
	if err := registry.Register(plan.HuntDefinition); err != nil {
		return err
	}
	hunt := library.NewHunt(p.library)
	executor.Handle(plan.JobType, plan.NewJob(plan.JobConfig{
		Hunt: hunt, Facts: p.planFacts, Clock: clock.NewSystem(), Logger: p.logger,
		Searchable: func(item library.PlanInput) bool { return music.SearchedOnItsOwn(item.SatisfactionKind) },
	}))
	executor.Handle(plan.HuntJobType, plan.NewHuntJob(plan.HuntConfig{
		Hunt: hunt, PerHour: p.searchesPerHour, Search: p.huntSearch, Clock: clock.NewSystem(), Logger: p.logger,
	}))
	if err := registry.Register(music.RSSDefinition); err != nil {
		return err
	}
	executor.Handle(music.RSSJobType, music.NewRSSJob(music.RSSConfig{
		Grab: p.musicGrab(), Indexers: p.indexerConns, WantedByArtist: hunt.WantedAlbumsByArtist,
		Clock: clock.NewSystem(), Logger: p.logger,
	}))
	return nil
}

func (p *process) enqueueRSS(ctx context.Context) (jobs.EnqueueResult, error) {
	return p.queue.Enqueue(ctx, jobs.Request{
		Type: music.RSSJobType, ActionKey: string(music.RSSJobType), Priority: jobs.PriorityNormal, Payload: []byte("{}"),
	})
}

// planFacts reads the priority policy and every library's rank fresh for one planner pass.
func (p *process) planFacts(context.Context) (plan.Facts, error) {
	snapshot := p.cache.Snapshot()
	stored := snapshot.PrioritySettings()
	weights := priority.DefaultWeights()
	weights.UserRequest, weights.NewRelease = stored.UserRequestWeight, stored.NewReleaseWeight
	weights.Gap, weights.Upgrade = stored.GapWeight, stored.UpgradeWeight
	ranks := make(map[int64]int64)
	for _, category := range snapshot.Categories() {
		ranks[category.ID] = category.PriorityRank
	}
	return plan.Facts{
		Policy: plan.Policy{Weights: weights, NewReleaseDays: int(stored.NewReleaseDays)},
		CategoryRank: func(categoryID int64) int64 {
			if rank, found := ranks[categoryID]; found {
				return rank
			}
			return 100
		},
	}, nil
}

// searchesPerHour is the hunt's budget; with no indexer enabled there is nothing to spend it on.
func (p *process) searchesPerHour(ctx context.Context) (int, error) {
	indexers, err := p.indexerConns(ctx)
	if err != nil || len(indexers) == 0 {
		return 0, err
	}
	return int(*p.cache.Snapshot().PrioritySettings().SearchesPerHour), nil
}

// huntSearch searches one album the hunt chose and sends the best release, if it is still wanted.
func (p *process) huntSearch(ctx context.Context, itemID int64) (plan.Outcome, error) {
	item, err := library.NewDesired(p.library).Get(ctx, itemID)
	if err != nil {
		return plan.OutcomeNotWanted, err
	}
	if item.Monitored != 1 || (item.State != "MISSING" && item.State != "PARTIAL") {
		return plan.OutcomeNotWanted, nil
	}
	if room, measured := p.cache.Snapshot().AcquisitionHeadroom(item.CategoryID); measured && room <= 0 {
		return plan.OutcomeNoRoom, nil
	}
	indexers, err := p.indexerConns(ctx)
	if err != nil {
		return plan.OutcomeSearched, err
	}
	result, err := p.musicGrab().SearchAndSend(ctx, itemID, indexers, music.GrabAutomatic, clock.NewSystem().Now())
	switch {
	case errors.Is(err, music.ErrTrackSearchNotSupported):
		return plan.OutcomeNotWanted, nil
	case err != nil:
		return plan.OutcomeSearched, err
	case result.AlreadyInFlight:
		return plan.OutcomeInFlight, nil
	}
	if result.Sent != nil || result.Held {
		p.logger.Info("hunt: release chosen", slog.Int64("item_id", itemID), slog.Bool("held", result.Held))
	}
	if len(result.Refusals) > 0 {
		p.logger.Info("hunt: releases refused", slog.Int64("item_id", itemID), slog.Any("refused", result.Refusals))
	}
	return plan.OutcomeSearched, nil
}

// searchStatus is an album page's F2 line: when it was and will be searched, and if not, why.
func (p *process) searchStatus(ctx context.Context, itemID int64) (apiserver.WantedRow, error) {
	item, err := library.NewDesired(p.library).Get(ctx, itemID)
	if err != nil {
		return apiserver.WantedRow{}, err
	}
	hunt := library.NewHunt(p.library)
	entry, planned, err := hunt.Entry(ctx, itemID)
	if err != nil {
		return apiserver.WantedRow{}, err
	}
	held, err := hunt.Held(ctx, itemID)
	if err != nil {
		return apiserver.WantedRow{}, err
	}
	_, inFlight, err := library.NewAcquisitions(p.library).InFlightForItem(ctx, itemID)
	if err != nil {
		return apiserver.WantedRow{}, err
	}
	indexers, err := p.indexerConns(ctx)
	if err != nil {
		return apiserver.WantedRow{}, err
	}
	snapshot := p.cache.Snapshot()
	room, measured := snapshot.AcquisitionHeadroom(item.CategoryID)
	now := clock.NewSystem().Now()
	why := plan.Explain(plan.Circumstances{
		Monitored: item.Monitored == 1, State: item.State, Searchable: music.SearchedOnItsOwn(item.SatisfactionKind),
		InFlight: inFlight, Held: held, Indexers: len(indexers), PerHour: int(*snapshot.PrioritySettings().SearchesPerHour),
		NoRoom: measured && room <= 0, Planned: planned && entry.EligibleAfter != plan.Never, ReleaseDate: entry.ReleaseDate,
	}, now)
	status := apiserver.WantedRow{LastSearchedAt: entry.LastSearchedAt, AutomaticSearch: string(why)}
	if why == plan.WhyScheduled || why == plan.WhyWaitingForRelease {
		// A search the planner has not replanned since still counts toward the backoff.
		status.NextSearchAt = max(entry.EligibleAfter, plan.RetryAfter(entry.LastSearchedAt, entry.SearchCount), now.Unix())
	}
	return status, nil
}

func (p *process) enqueuePlan(ctx context.Context) (jobs.EnqueueResult, error) {
	return p.queue.Enqueue(ctx, jobs.Request{
		Type: plan.JobType, ActionKey: string(plan.JobType), Priority: jobs.PriorityLow, Payload: []byte("{}"),
	})
}

func (p *process) enqueueHunt(ctx context.Context) (jobs.EnqueueResult, error) {
	return p.queue.Enqueue(ctx, jobs.Request{
		Type: plan.HuntJobType, ActionKey: string(plan.HuntJobType), Priority: jobs.PriorityLow, Payload: []byte("{}"),
	})
}
