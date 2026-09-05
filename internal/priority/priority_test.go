package priority_test

import (
	"testing"
	"time"

	"github.com/makearr/MediaNexus/internal/priority"
)

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// The trigger ladder must dominate category rank: a user_request in the
// least-preferred category still outranks a gap in the most-preferred one.
func TestCompute_TriggerOrderingDominatesCategoryRank(t *testing.T) {
	weights := priority.DefaultWeights()

	userRequestLowRank := priority.Compute(weights, priority.Input{
		Trigger: priority.TriggerUserRequest, CategoryRank: 6, QueuedSince: epoch,
	}, epoch)
	gapTopRank := priority.Compute(weights, priority.Input{
		Trigger: priority.TriggerGap, CategoryRank: 1, QueuedSince: epoch,
	}, epoch)

	if userRequestLowRank <= gapTopRank {
		t.Errorf("user_request (rank 6) = %d, gap (rank 1) = %d, want user_request to still win",
			userRequestLowRank, gapTopRank)
	}
}

// The full ladder, same category rank throughout: user request > new release
// > gap > upgrade.
func TestCompute_FullTriggerLadder(t *testing.T) {
	weights := priority.DefaultWeights()
	score := func(trigger priority.Trigger) int64 {
		return priority.Compute(weights, priority.Input{
			Trigger: trigger, CategoryRank: 3, QueuedSince: epoch,
		}, epoch)
	}

	userRequest, newRelease := score(priority.TriggerUserRequest), score(priority.TriggerNewRelease)
	gap, upgrade := score(priority.TriggerGap), score(priority.TriggerUpgrade)
	descending := userRequest > newRelease && newRelease > gap && gap > upgrade
	if !descending {
		t.Errorf("ladder = [%d, %d, %d, %d], want strictly descending user_request > new_release > gap > upgrade",
			userRequest, newRelease, gap, upgrade)
	}
}

// Within the same trigger, a better (lower-numbered) category rank must win.
func TestCompute_CategoryRankOrdersWithinATrigger(t *testing.T) {
	weights := priority.DefaultWeights()
	topRank := priority.Compute(weights, priority.Input{
		Trigger: priority.TriggerNewRelease, CategoryRank: 1, QueuedSince: epoch,
	}, epoch)
	bottomRank := priority.Compute(weights, priority.Input{
		Trigger: priority.TriggerNewRelease, CategoryRank: 6, QueuedSince: epoch,
	}, epoch)
	if topRank <= bottomRank {
		t.Errorf("rank 1 = %d, rank 6 = %d, want rank 1 (most preferred) to score higher", topRank, bottomRank)
	}
}

// Modifiers (subscription boost, a manual pin) must move the score without
// this package caring what they represent.
func TestCompute_ModifiersAddIn(t *testing.T) {
	weights := priority.DefaultWeights()
	base := priority.Compute(weights, priority.Input{
		Trigger: priority.TriggerGap, CategoryRank: 3, QueuedSince: epoch,
	}, epoch)
	pinned := priority.Compute(weights, priority.Input{
		Trigger: priority.TriggerGap, CategoryRank: 3, Modifiers: 50, QueuedSince: epoch,
	}, epoch)
	if pinned != base+50 {
		t.Errorf("pinned = %d, base = %d, want exactly +50", pinned, base)
	}
}

// The acceptance criterion this block was built for: aging must actually be
// able to overtake a higher trigger, or a steady stream of new releases
// starves every gap forever. A gap item queued long enough must eventually
// outrank a new_release item that just arrived.
func TestCompute_AgingEventuallyOvertakesAHigherTrigger(t *testing.T) {
	weights := priority.DefaultWeights()
	freshNewRelease := priority.Compute(weights, priority.Input{
		Trigger: priority.TriggerNewRelease, CategoryRank: 1, QueuedSince: epoch,
	}, epoch)

	// One trigger step is 1000 * (NewRelease - Gap) = 10,000 points apart, at
	// 1 point per AgingInterval (an hour) that is over a year of waiting --
	// a real number, not hand-picked to just barely pass.
	longQueued := epoch.Add(-2 * 365 * 24 * time.Hour)
	agedGap := priority.Compute(weights, priority.Input{
		Trigger: priority.TriggerGap, CategoryRank: 1, QueuedSince: longQueued,
	}, epoch)

	if agedGap <= freshNewRelease {
		t.Errorf("aged gap = %d, fresh new_release = %d, want the long-waiting gap to have overtaken it",
			agedGap, freshNewRelease)
	}

	// And a gap that just entered the queue must NOT have overtaken it --
	// this is what proves aging is doing the work, not the formula being
	// broken in some other way that happens to pass the assertion above.
	freshGap := priority.Compute(weights, priority.Input{
		Trigger: priority.TriggerGap, CategoryRank: 1, QueuedSince: epoch,
	}, epoch)
	if freshGap >= freshNewRelease {
		t.Errorf("fresh gap = %d, fresh new_release = %d, want new_release to still win with no aging yet",
			freshGap, freshNewRelease)
	}
}

// The literal acceptance test this block was specified with: run a skewed
// queue forward until the lowest-priority item reaches the front, against
// the adversarial case architecture.md's own text names -- "a steady stream
// of high-rank items means the bottom of the list never acquires anything".
// Every tick, a brand-new user_request (the highest trigger, zero aging)
// arrives to compete; the one gap item queued at the very start never gets
// searched, never gets removed, and just keeps aging. If it never reaches
// the front despite that, the anti-starvation guarantee is broken.
func TestCompute_ASkewedQueueEventuallyPutsTheLowestPriorityItemFirst(t *testing.T) {
	weights := priority.DefaultWeights()
	stuckSince := epoch

	tick := time.Hour
	const maxTicks = 30000 // ~3.4 years of simulated waiting, a generous but bounded ceiling
	for i := 0; i < maxTicks; i++ {
		now := epoch.Add(time.Duration(i) * tick)

		stuckGap := priority.Compute(weights, priority.Input{
			Trigger: priority.TriggerGap, CategoryRank: 6, QueuedSince: stuckSince,
		}, now)
		// A fresh competitor arrives at this exact tick, so it never has any
		// aging of its own to lose -- the worst case for the stuck item.
		freshUserRequest := priority.Compute(weights, priority.Input{
			Trigger: priority.TriggerUserRequest, CategoryRank: 1, QueuedSince: now,
		}, now)

		if stuckGap > freshUserRequest {
			return // reached the front -- the guarantee holds
		}
	}
	t.Fatalf("the stuck gap item never overtook a freshly-arriving user_request within %d simulated hours",
		maxTicks)
}

func TestClassifyTrigger_WithinTheWindowIsNewRelease(t *testing.T) {
	releaseDate := epoch.Add(-10 * 24 * time.Hour)
	if got := priority.ClassifyTrigger(&releaseDate, epoch, priority.DefaultNewReleaseDays); got != priority.TriggerNewRelease {
		t.Errorf("ClassifyTrigger = %q, want new_release for a 10-day-old release", got)
	}
}

func TestClassifyTrigger_OutsideTheWindowIsGap(t *testing.T) {
	releaseDate := epoch.Add(-365 * 24 * time.Hour)
	if got := priority.ClassifyTrigger(&releaseDate, epoch, priority.DefaultNewReleaseDays); got != priority.TriggerGap {
		t.Errorf("ClassifyTrigger = %q, want gap for a year-old release", got)
	}
}

// A twenty-year-old back catalogue added today by a fresh subscription must
// classify as a gap, not new_release -- the exact scenario ADR-36's own
// amendment names as the reason "new" is measured from the release date, not
// from when the item was added.
func TestClassifyTrigger_OldReleaseAddedTodayIsStillAGap(t *testing.T) {
	releaseDate := time.Date(2006, 1, 1, 0, 0, 0, 0, time.UTC)
	if got := priority.ClassifyTrigger(&releaseDate, epoch, priority.DefaultNewReleaseDays); got != priority.TriggerGap {
		t.Errorf("ClassifyTrigger = %q, want gap for a 20-year-old release regardless of when it was added", got)
	}
}

// An unreleased item (or one with no known date at all) is new_release --
// ADR-23's AvailabilityWindow is what gates the search before it exists, not
// this classification.
func TestClassifyTrigger_UnreleasedOrUnknownIsNewRelease(t *testing.T) {
	future := epoch.Add(30 * 24 * time.Hour)
	if got := priority.ClassifyTrigger(&future, epoch, priority.DefaultNewReleaseDays); got != priority.TriggerNewRelease {
		t.Errorf("ClassifyTrigger(future) = %q, want new_release", got)
	}
	if got := priority.ClassifyTrigger(nil, epoch, priority.DefaultNewReleaseDays); got != priority.TriggerNewRelease {
		t.Errorf("ClassifyTrigger(nil) = %q, want new_release", got)
	}
}
