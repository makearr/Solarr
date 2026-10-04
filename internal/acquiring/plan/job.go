package plan

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// JobType is one planner pass over the dirty set and a slice of the sweep (section 9.1).
const JobType = jobs.Type("acquire.plan")

// Definition runs one pass at a time; the next scheduled pass is its retry.
var Definition = jobs.Definition{
	Name: "Planning wanted albums", Type: JobType, Pool: jobs.PoolSearch, Mutating: true, MaxAttempts: 1,
}

// dirtyBudget is section 9.1's items_per_tick; sweepSlices spreads the sweep over a day of 5-minute passes.
const (
	dirtyBudget = 500
	sweepSlices = 288
)

// Facts is what a pass needs from settings, read fresh each pass.
type Facts struct {
	Policy Policy
	// CategoryRank answers a library's priority rank (ADR-36); lower is more important.
	CategoryRank func(categoryID int64) int64
	// Upgrade answers a library's upgrade hysteresis.
	Upgrade func(categoryID int64) UpgradeRules
}

// upgradeRules is Upgrade, or no hysteresis for a Facts that states none.
func (f Facts) upgradeRules(categoryID int64) UpgradeRules {
	if f.Upgrade == nil {
		return UpgradeRules{}
	}
	return f.Upgrade(categoryID)
}

// JobConfig is what a Job needs; every field but Logger is required.
type JobConfig struct {
	Hunt  library.Hunt
	Facts func(ctx context.Context) (Facts, error)
	// Searchable is the media type's rule for which items are searched on their own (ADR-23).
	Searchable func(item library.PlanInput) bool
	Clock      clock.Clock
	Logger     *slog.Logger
}

// Job is the planner (ADR-6): it reads desired items and writes hunt_queue, and never searches.
type Job struct {
	config JobConfig
	mu     sync.Mutex
	// sweepAfter is the paced sweep's cursor; a restart begins the sweep again, which is harmless.
	sweepAfter int64
}

// NewJob builds the planner.
func NewJob(config JobConfig) *Job {
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	return &Job{config: config}
}

// Run plans the dirty set, then a slice of every item so drift and passing dates are caught.
func (j *Job) Run(ctx context.Context, _ jobs.Job) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	facts, err := j.config.Facts(ctx)
	if err != nil {
		return fmt.Errorf("plan: settings: %w", err)
	}
	now := j.config.Clock.Now()
	dirty, err := j.config.Hunt.PlanInputs(ctx, true, 0, dirtyBudget, now.Add(-UpgradeWindow).Unix())
	if err != nil {
		return err
	}
	planned, err := j.apply(ctx, dirty, facts, now)
	if err != nil {
		return err
	}
	swept, err := j.sweep(ctx, facts, now)
	if err != nil {
		return err
	}
	if len(dirty) > 0 {
		j.config.Logger.Info("plan: pass done", "dirty", len(dirty), "planned", planned, "swept", swept)
	}
	return nil
}

func (j *Job) sweep(ctx context.Context, facts Facts, now time.Time) (int, error) {
	maxID, err := j.config.Hunt.MaxItemID(ctx)
	if err != nil {
		return 0, err
	}
	if j.sweepAfter >= maxID {
		j.sweepAfter = 0
	}
	slice := max((maxID+sweepSlices-1)/sweepSlices, 50)
	inputs, err := j.config.Hunt.PlanInputs(ctx, false, j.sweepAfter, slice, now.Add(-UpgradeWindow).Unix())
	if err != nil {
		return 0, err
	}
	if len(inputs) > 0 {
		j.sweepAfter = inputs[len(inputs)-1].ID
	}
	return j.apply(ctx, inputs, facts, now)
}

func (j *Job) apply(ctx context.Context, inputs []library.PlanInput, facts Facts, now time.Time) (int, error) {
	planned := make([]library.PlannedHunt, len(inputs))
	for index, input := range inputs {
		entry := Decide(Item{
			ID: input.ID, Monitored: input.Monitored, State: input.State,
			Searchable: j.config.Searchable(input), ReleaseDate: input.ReleaseDate,
			CategoryRank: facts.CategoryRank(input.CategoryID), LastSearchedAt: input.LastSearchedAt,
			SearchCount: input.SearchCount, QueuedSince: input.QueuedSince, AfterFailure: input.AfterFailure,
			ArrivedAt: input.ArrivedAt, RecentImports: input.RecentImports, RSSSeenAt: input.RSSSeenAt, Upgrade: facts.upgradeRules(input.CategoryID),
		}, facts.Policy, now)
		planned[index] = library.PlannedHunt{
			Input: input, Class: entry.Class, Priority: entry.Priority, ReleaseDate: entry.ReleaseDate,
			EligibleAfter: entry.EligibleAfter, QueuedSince: entry.QueuedSince,
		}
	}
	return j.config.Hunt.ApplyPlan(ctx, planned, now.Unix())
}

// SearchedOnItsOwn says which desired items the hunt searches for: a SET (an album, a season), never one of its parts.
func SearchedOnItsOwn(satisfactionKind string) bool { return satisfactionKind == "SET" }
