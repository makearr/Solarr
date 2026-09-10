// Package priority computes ADR-36's single composite acquisition priority --
// pure arithmetic over caller-supplied facts, no I/O and no database of its
// own, the same separation internal/identify/score and internal/moving/placement keep from
// their own callers. What creates a hunt_queue row and reads this number back
// out is stage 5/6's planner, which does not exist yet (§10.5's "ready
// queue"); this package is ready ahead of that consumer, the same shape
// internal/moving/placement and internal/moving/naming already were before acquisition
// existed to call them.
package priority

import "time"

// Trigger is why an item is being searched for at all -- ADR-36's own ladder,
// user request first.
type Trigger string

// The four triggers, in the order the shipped defaults rank them.
const (
	TriggerUserRequest Trigger = "user_request"
	TriggerNewRelease  Trigger = "new_release"
	TriggerGap         Trigger = "gap"
	TriggerUpgrade     Trigger = "upgrade"
)

// Weights are the four trigger weights -- ADR-36's 2026-09-05 amendment made
// these advanced-tier settings rather than constants, so a user who wants
// gaps filled before new releases changes two numbers rather than losing the
// feature. Bigger means more urgent.
type Weights struct {
	UserRequest int64
	NewRelease  int64
	Gap         int64
	Upgrade     int64
}

// DefaultWeights is the shipped ladder: user request > new release > gap >
// upgrade. Spaced apart deliberately, not just 4/3/2/1 -- see the package's
// own tests for why the gap matters once modifiers are added in.
func DefaultWeights() Weights {
	return Weights{UserRequest: 40, NewRelease: 30, Gap: 20, Upgrade: 10}
}

func (w Weights) forTrigger(t Trigger) int64 {
	switch t {
	case TriggerUserRequest:
		return w.UserRequest
	case TriggerNewRelease:
		return w.NewRelease
	case TriggerGap:
		return w.Gap
	case TriggerUpgrade:
		return w.Upgrade
	default:
		return 0
	}
}

// AgingInterval is how much waiting is worth one point of priority, the same
// linear-aging shape internal/db/store's own write queue uses for the same
// reason (section 4.2): without it, a steady stream of high-rank items means
// the bottom of the list never acquires anything.
const AgingInterval = time.Hour

// DefaultNewReleaseDays is ADR-36's own default: an item is "new" for 30 days
// after its release date, not from when it was added to the library.
const DefaultNewReleaseDays = 30

// Input is what one hunt_queue-shaped item needs computed. CategoryRank is
// 1-based and user-ordered (drag-to-reorder in settings): 1 is the most
// preferred category, matching the list a user actually sees and edits.
type Input struct {
	Trigger      Trigger
	CategoryRank int64
	// Modifiers is the caller's own sum of subscription boost, a manual pin,
	// and anything else additive -- this package has no opinion on their
	// individual sizes, only that they add in before aging does.
	Modifiers int64
	// QueuedSince is when this item first became eligible to search --
	// aging is measured from here, not from when it was created or last
	// searched (a distinct concept hunt_queue's own last_searched_at already
	// carries for backoff, section 9.3).
	QueuedSince time.Time
}

// Compute returns ADR-36's single composite priority at now. Bigger sorts
// first (hunt_queue's own index is priority DESC, section 5.3's schema).
//
// CategoryRank is negated rather than inverted against a category count: rank
// 1 (most preferred) becomes -100, rank 6 becomes -600, and -100 > -600 sorts
// rank 1 first under the same DESC order the trigger term already assumes --
// without this package ever needing to know how many categories exist.
func Compute(weights Weights, input Input, now time.Time) int64 {
	triggerScore := weights.forTrigger(input.Trigger) * 1000
	categoryScore := -input.CategoryRank * 100
	agingBonus := int64(now.Sub(input.QueuedSince) / AgingInterval)
	return triggerScore + categoryScore + input.Modifiers + agingBonus
}

// ClassifyTrigger decides new_release vs gap from a release date (ADR-36's
// 2026-09-05 amendment) -- user_request and upgrade are never derived this
// way, since they come from why the caller is searching at all, not from a
// date. A nil or future releaseDate is new_release either way: an unreleased
// item costs nothing to rank highly, since ADR-23's AvailabilityWindow is
// what actually gates the search before the item exists, not this
// classification.
func ClassifyTrigger(releaseDate *time.Time, now time.Time, newReleaseDays int) Trigger {
	if releaseDate == nil || releaseDate.After(now) {
		return TriggerNewRelease
	}
	if now.Sub(*releaseDate) <= time.Duration(newReleaseDays)*24*time.Hour {
		return TriggerNewRelease
	}
	return TriggerGap
}
