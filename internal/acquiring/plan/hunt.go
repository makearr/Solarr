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

// HuntJobType is one automatic search run over the hunt queue (section 9.3, 9.4).
const HuntJobType = jobs.Type("acquire.hunt")

// HuntDefinition runs one hunt at a time; the next scheduled run is its retry.
var HuntDefinition = jobs.Definition{
	Name: "Searching for missing albums", Type: HuntJobType, Pool: jobs.PoolSearch, Mutating: true, MaxAttempts: 1,
}

// Outcome is what searching one item came to, as the hunt needs to know it.
type Outcome int

// The outcomes: a search ran, a download is already on its way, the item is not to be searched,
// or its library has no room for a download (ADR-12).
const (
	OutcomeSearched Outcome = iota
	OutcomeInFlight
	OutcomeNotWanted
	OutcomeNoRoom
)

// budgetWindow is section 9.4's window; inFlightRecheck is how soon an item with a download on its way is looked at again.
const (
	budgetWindow    = time.Hour
	inFlightRecheck = time.Hour
)

// HuntConfig is what a HuntJob needs; every field but Logger is required.
type HuntConfig struct {
	Hunt library.Hunt
	// PerHour is the search budget (section 9.4); zero turns automatic searching off.
	PerHour func(ctx context.Context) (int, error)
	// Search searches one item and sends what it finds, re-checking first that it is still wanted.
	Search func(ctx context.Context, itemID int64) (Outcome, error)
	Clock  clock.Clock
	Logger *slog.Logger
}

// HuntJob searches the most urgent eligible items, within the hourly budget.
type HuntJob struct {
	config HuntConfig
	mu     sync.Mutex
	// searched is when each search of the last window ran; a restart forgets it, costing one window at most.
	searched []time.Time
}

// NewHuntJob builds the hunt.
func NewHuntJob(config HuntConfig) *HuntJob {
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	return &HuntJob{config: config}
}

// Run searches as many eligible items as the budget has left this window.
func (j *HuntJob) Run(ctx context.Context, job jobs.Job) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	perHour, err := j.config.PerHour(ctx)
	if err != nil {
		return fmt.Errorf("plan: hunt budget: %w", err)
	}
	now := j.config.Clock.Now()
	j.forgetBefore(now.Add(-budgetWindow))
	left := perHour - len(j.searched)
	if left <= 0 {
		return nil
	}
	// Section 9.4: each class gets its share first, then what one leaves unused goes to the other.
	upgradeShare := (left*upgradeSharePercent + 99) / 100
	shares := []struct {
		class  string
		budget int
	}{{ClassMissing, left - upgradeShare}, {ClassUpgrade, upgradeShare}}
	searched, looked := 0, 0
	for _, share := range shares {
		done, seen, err := j.huntClass(ctx, job, share.class, min(share.budget, left-searched), now, looked)
		if err != nil {
			return err
		}
		searched, looked = searched+done, looked+seen
	}
	for _, share := range shares {
		done, seen, err := j.huntClass(ctx, job, share.class, left-searched, now, looked)
		if err != nil {
			return err
		}
		searched, looked = searched+done, looked+seen
	}
	if looked > 0 {
		j.config.Logger.Info("plan: hunt done", "looked", looked, "searched", searched, "budget_left", left-searched)
	}
	return nil
}

// upgradeSharePercent is the part of each window's budget that upgrades are promised (section 9.4).
const upgradeSharePercent = 20

// huntClass searches up to budget eligible items of one class; answers how many searches ran and how many items were looked at.
func (j *HuntJob) huntClass(
	ctx context.Context, job jobs.Job, class string, budget int, now time.Time, lookedBefore int,
) (searched, looked int, err error) {
	// Every item looked at is postponed past now, so each page is new; the cap guards a failing postpone.
	for searched < budget && looked < 10*budget+100 {
		ids, err := j.config.Hunt.NextToHunt(ctx, class, now.Unix(), int64(budget-searched))
		if err != nil {
			return searched, looked, err
		}
		if len(ids) == 0 {
			break
		}
		job.Progress.SetTotal(int64(lookedBefore + looked + len(ids)))
		for _, itemID := range ids {
			if err := ctx.Err(); err != nil {
				return searched, looked, err
			}
			searched += j.huntOne(ctx, itemID)
			looked++
			job.Progress.Add(1)
		}
	}
	return searched, looked, nil
}

// huntOne searches itemID and keeps the hunt off it until it is worth looking again; answers 1 when a search ran.
func (j *HuntJob) huntOne(ctx context.Context, itemID int64) int {
	outcome, err := j.config.Search(ctx, itemID)
	now := j.config.Clock.Now()
	next, spent := now.Add(firstBackoff).Unix(), 1
	switch {
	case err != nil:
		j.config.Logger.Warn("plan: hunt: search failed", "item_id", itemID, "error", err)
		next = now.Add(inFlightRecheck).Unix()
	case outcome == OutcomeInFlight, outcome == OutcomeNoRoom:
		next, spent = now.Add(inFlightRecheck).Unix(), 0
	case outcome == OutcomeNotWanted:
		next, spent = Never, 0
	}
	if spent > 0 {
		j.searched = append(j.searched, now)
	}
	if err := j.config.Hunt.Postpone(ctx, itemID, next); err != nil {
		j.config.Logger.Warn("plan: hunt: postpone failed", "item_id", itemID, "error", err)
	}
	return spent
}

func (j *HuntJob) forgetBefore(cutoff time.Time) {
	kept := j.searched[:0]
	for _, at := range j.searched {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	j.searched = kept
}
