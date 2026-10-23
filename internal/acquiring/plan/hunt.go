package plan

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"slices"
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
	Name: "Searching for what is missing", Type: HuntJobType, Pool: jobs.PoolSearch, Mutating: true, MaxAttempts: 1,
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
	// Mode is the order eligible items are taken in; nil is oldest_searched.
	Mode func(ctx context.Context) (library.HuntMode, error)
	// NewReleaseDays is the freshness window that tells a new release from a gap; nil is 7.
	NewReleaseDays func() int
	// Interval is how often the hunt runs; each run spends its part of the hourly budget. Nil spends it all at once.
	Interval func() time.Duration
	// Libraries are the libraries searched and their shares; nil searches every library as one.
	Libraries func(ctx context.Context) ([]LibraryShare, error)
	// Search searches one item and sends what it finds, re-checking first that it is still wanted.
	Search func(ctx context.Context, itemID int64) (Outcome, error)
	Clock  clock.Clock
	Logger *slog.Logger
}

// LibraryShare is a library's weight in each run's searches; only the ratios between libraries matter.
type LibraryShare struct {
	CategoryID int64
	Share      int64
}

// HuntJob searches the most urgent eligible items, within the hourly budget.
type HuntJob struct {
	config HuntConfig
	mu     sync.Mutex
	// searched is when each search of the last window ran; a restart forgets it, costing one window at most.
	searched []time.Time
	// credit is each library's standing in splitByShare's round robin.
	credit map[int64]int64
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
	left := min(perHour-len(j.searched), j.perRun(perHour))
	if left <= 0 {
		return nil
	}
	libraries := []LibraryShare{{Share: 1}}
	if j.config.Libraries != nil {
		if libraries, err = j.config.Libraries(ctx); err != nil {
			return fmt.Errorf("plan: hunt libraries: %w", err)
		}
		libraries = slices.DeleteFunc(libraries, func(library LibraryShare) bool { return library.Share <= 0 })
		slices.SortStableFunc(libraries, func(a, b LibraryShare) int { return cmp.Compare(b.Share, a.Share) })
	}
	mode := library.HuntOldestSearched
	if j.config.Mode != nil {
		if mode, err = j.config.Mode(ctx); err != nil {
			return fmt.Errorf("plan: hunt mode: %w", err)
		}
	}
	days := 7
	if j.config.NewReleaseDays != nil {
		days = j.config.NewReleaseDays()
	}
	freshFrom := now.Add(-time.Duration(days) * 24 * time.Hour).Unix()
	// Section 9.4: each share gets its part of the budget first, then what one leaves unused goes to the others.
	shares := []share{
		{class: ClassMissing, releaseFrom: freshFrom},
		{class: ClassMissing, releaseBefore: freshFrom},
		{class: ClassUpgrade},
	}
	searched, looked := 0, 0
	// Rounded up for the missing shares, so a budget of one goes to a missing album, not to an upgrade.
	budgets := []int{(left*newReleasePercent + 99) / 100, 0, 0}
	budgets[1] = min((left*gapPercent+99)/100, left-budgets[0])
	budgets[2] = left - budgets[0] - budgets[1]
	// Each share is split over the libraries by their weights (user, 2026-10-23); a library that runs out of a share's
	// items is left out of that share's later deals.
	exhausted := make([]map[int64]bool, len(shares))
	spend := func(index int, budget int) error {
		if exhausted[index] == nil {
			exhausted[index] = map[int64]bool{}
		}
		open := slices.DeleteFunc(slices.Clone(libraries), func(library LibraryShare) bool { return exhausted[index][library.CategoryID] })
		parts := j.splitByShare(budget, open)
		for position, library := range open {
			part := min(parts[position], left-searched)
			if part <= 0 {
				continue
			}
			done, seen, err := j.huntShare(ctx, job, shares[index], library.CategoryID, mode, part, now, looked)
			if err != nil {
				return err
			}
			if done < part {
				exhausted[index][library.CategoryID] = true
			}
			searched, looked = searched+done, looked+seen
		}
		return nil
	}
	for index := range shares {
		if err := spend(index, min(budgets[index], left-searched)); err != nil {
			return err
		}
	}
	// What a share or a library leaves unused goes to the others, split the same way, until nothing more is found.
	for round := 0; searched < left && round <= len(shares)*len(libraries); round++ {
		before := searched
		for index := range shares {
			if err := spend(index, left-searched); err != nil {
				return err
			}
		}
		if searched == before {
			break
		}
	}
	if looked > 0 {
		j.config.Logger.Info("plan: hunt done", "looked", looked, "searched", searched, "budget_left", left-searched)
	}
	return nil
}

// perRun is one run's part of the hourly budget, rounded up so a small budget still searches.
func (j *HuntJob) perRun(perHour int) int {
	if j.config.Interval == nil {
		return perHour
	}
	interval := j.config.Interval()
	if interval <= 0 || interval >= budgetWindow {
		return perHour
	}
	return int((int64(perHour)*int64(interval) + int64(budgetWindow) - 1) / int64(budgetWindow))
}

// splitByShare deals budget out one search at a time by smooth weighted round robin over the libraries' shares; the
// credits carry over between runs, so even a budget of one per run comes out in proportion over time.
func (j *HuntJob) splitByShare(budget int, libraries []LibraryShare) []int {
	parts := make([]int, len(libraries))
	var total int64
	for _, library := range libraries {
		total += library.Share
	}
	if total == 0 {
		return parts
	}
	if j.credit == nil {
		j.credit = map[int64]int64{}
	}
	for range max(budget, 0) {
		best := 0
		for index, library := range libraries {
			j.credit[library.CategoryID] += library.Share
			if j.credit[library.CategoryID] > j.credit[libraries[best].CategoryID] {
				best = index
			}
		}
		j.credit[libraries[best].CategoryID] -= total
		parts[best]++
	}
	return parts
}

// Section 9.4's split of each window's budget: new releases, gaps and upgrades.
const (
	newReleasePercent = 40
	gapPercent        = 40
)

// share is one slice of the queue the budget is split over: a class, and for missing albums the release-date range
// that makes them new releases or gaps.
type share struct {
	class                      string
	releaseFrom, releaseBefore int64
}

// huntShare searches up to budget eligible items of one share in one library (0: all); answers how many searches ran
// and how many items were looked at.
func (j *HuntJob) huntShare(
	ctx context.Context, job jobs.Job, one share, categoryID int64, mode library.HuntMode, budget int, now time.Time, lookedBefore int,
) (searched, looked int, err error) {
	// Every item looked at is postponed past now, so each page is new; the cap guards a failing postpone.
	for searched < budget && looked < 10*budget+100 {
		ids, err := j.config.Hunt.NextToHunt(ctx, library.HuntSelection{
			Class: one.class, Mode: mode, Now: now.Unix(), Limit: int64(budget - searched),
			ReleaseFrom: one.releaseFrom, ReleaseBefore: one.releaseBefore, CategoryID: categoryID,
		})
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
