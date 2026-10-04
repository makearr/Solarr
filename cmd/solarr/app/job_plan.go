package app

import (
	"context"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/plan"
	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/priority"
	"github.com/makearr/Solarr/internal/identify/score"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media"
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
		Searchable: func(item library.PlanInput) bool { return plan.SearchedOnItsOwn(item.SatisfactionKind) },
	}))
	executor.Handle(plan.HuntJobType, plan.NewHuntJob(plan.HuntConfig{
		Hunt: hunt, PerHour: p.searchesPerHour, Search: p.huntSearch,
		Mode: func(context.Context) (library.HuntMode, error) {
			return library.HuntMode(p.cache.Snapshot().PrioritySettings().HuntMode), nil
		},
		NewReleaseDays: func() int { return int(p.cache.Snapshot().PrioritySettings().NewReleaseDays) }, Clock: clock.NewSystem(), Logger: p.logger,
	}))
	return nil
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
		Policy:  plan.Policy{Weights: weights, NewReleaseDays: int(stored.NewReleaseDays), RSSFreshHours: int(*stored.RSSFreshHours)},
		Upgrade: p.upgradeRules(snapshot),
		CategoryRank: func(categoryID int64) int64 {
			if rank, found := ranks[categoryID]; found {
				return rank
			}
			return 100
		},
	}, nil
}

// upgradeRules reads each library's upgrade hysteresis from its first quality profile (section 9.2), the
// shipped 24 hours and three imports where it states none.
func (p *process) upgradeRules(snapshot *settings.Snapshot) func(categoryID int64) plan.UpgradeRules {
	rules := make(map[int64]plan.UpgradeRules)
	for _, category := range snapshot.Categories() {
		found := plan.UpgradeRules{MinFileAge: 24 * time.Hour, MaxPerWindow: 3}
		if len(category.QualityProfileIDs) > 0 {
			if profile, ok := snapshot.QualityProfile(category.QualityProfileIDs[0]); ok {
				if policy, err := score.ParseUpgrade([]byte(profile.UpgradeJSON)); err == nil {
					if policy.MinFileAge > 0 {
						found.MinFileAge = time.Duration(policy.MinFileAge)
					}
					if policy.MaxPerWindow > 0 {
						found.MaxPerWindow = int64(policy.MaxPerWindow)
					}
				}
			}
		}
		rules[category.ID] = found
	}
	return func(categoryID int64) plan.UpgradeRules {
		if found, ok := rules[categoryID]; ok {
			return found
		}
		return plan.UpgradeRules{MinFileAge: 24 * time.Hour, MaxPerWindow: 3}
	}
}

// searchesPerHour is the hunt's budget; with no indexer enabled there is nothing to spend it on.
func (p *process) searchesPerHour(ctx context.Context) (int, error) {
	indexers, err := p.indexerConns(ctx)
	if err != nil || len(indexers) == 0 {
		return 0, err
	}
	return int(*p.cache.Snapshot().PrioritySettings().SearchesPerHour), nil
}

// huntSearch hands one item the hunt chose to its library's media type, if it is still wanted and there is room.
func (p *process) huntSearch(ctx context.Context, itemID int64) (plan.Outcome, error) {
	item, err := library.NewDesired(p.library).Get(ctx, itemID)
	if err != nil {
		return plan.OutcomeNotWanted, err
	}
	if item.Monitored != 1 || (item.State != "MISSING" && item.State != "PARTIAL" && item.State != "UPGRADABLE") {
		return plan.OutcomeNotWanted, nil
	}
	if room, measured := p.cache.Snapshot().AcquisitionHeadroom(item.CategoryID); measured && room <= 0 {
		return plan.OutcomeNoRoom, nil
	}
	category, found := p.cache.Snapshot().Category(item.CategoryID)
	if !found {
		return plan.OutcomeNotWanted, nil
	}
	hunting, ok := media.Capability[media.Hunting](p.media, media.Key(category.MediaType))
	if !ok {
		return plan.OutcomeNotWanted, nil
	}
	return hunting.HuntSearch(ctx, itemID)
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
		Monitored: item.Monitored == 1, State: item.State, Searchable: plan.SearchedOnItsOwn(item.SatisfactionKind),
		InFlight: inFlight, Held: held, Indexers: len(indexers), PerHour: int(*snapshot.PrioritySettings().SearchesPerHour),
		NoRoom: measured && room <= 0, Planned: planned && entry.EligibleAfter != plan.Never, ReleaseDate: entry.ReleaseDate,
	}, now)
	status := apiserver.WantedRow{LastSearchedAt: entry.LastSearchedAt, AutomaticSearch: string(why)}
	if why == plan.WhyScheduled || why == plan.WhyWaitingForRelease {
		// A search the planner has not replanned since still counts toward the backoff.
		status.NextSearchAt = max(entry.EligibleAfter, plan.RetryAfter(entry.LastSearchedAt, entry.SearchCount, entry.AfterFailure), now.Unix())
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
