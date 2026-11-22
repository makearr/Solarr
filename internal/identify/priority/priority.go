// Package priority computes ADR-36's composite acquisition priority: pure arithmetic over supplied facts, no I/O or database.
package priority

import "time"

// Trigger is why an item is being searched for at all -- ADR-36's own ladder,
// user request first.
type Trigger string

// The triggers, in the order the shipped defaults rank them.
const (
	TriggerUserRequest Trigger = "user_request"
	// TriggerRSS is a release an RSS sync found for a wanted album (2026-09-29: user > RSS > new).
	TriggerRSS        Trigger = "rss"
	TriggerNewRelease Trigger = "new_release"
	TriggerGap        Trigger = "gap"
	TriggerUpgrade    Trigger = "upgrade"
)

// Weights are the trigger weights, advanced-tier settings (ADR-36 amendment) so a user can favour gaps over new releases; bigger is more urgent.
type Weights struct {
	UserRequest int64
	RSS         int64
	NewRelease  int64
	Gap         int64
	Upgrade     int64
}

// DefaultWeights is the shipped ladder: user request > RSS > new release > gap >
// upgrade. Spaced apart deliberately, not just 4/3/2/1 -- see the package's
// own tests for why the gap matters once modifiers are added in.
func DefaultWeights() Weights {
	return Weights{UserRequest: 40, RSS: 35, NewRelease: 30, Gap: 20, Upgrade: 10}
}

func (w Weights) forTrigger(t Trigger) int64 {
	switch t {
	case TriggerUserRequest:
		return w.UserRequest
	case TriggerRSS:
		return w.RSS
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

// AgingInterval is how much waiting is worth one priority point, linear aging like the store's write queue, so low-rank items are not starved (section 4.2).
const AgingInterval = time.Hour

// DefaultNewReleaseDays is ADR-55's freshness window: new for 7 days from the release date, so older albums compete as gaps; the RSS window softens that.
const DefaultNewReleaseDays = 7

// RSSFreshHours is ADR-55's availability window: an item an RSS feed showed a release for is new to this library
// for this many hours, whatever its release date. A fixed value until the setting is registered.
const RSSFreshHours = 48

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
	// QueuedSince is when the item first became eligible; aging runs from here, not creation or last search (hunt_queue.last_searched_at serves backoff, section 9.3).
	QueuedSince time.Time
}

// Compute returns ADR-36's priority at now, bigger first (hunt_queue index is priority DESC); CategoryRank is negated so rank 1 sorts first without knowing the category count.
func Compute(weights Weights, input Input, now time.Time) int64 {
	triggerScore := weights.forTrigger(input.Trigger) * 1000
	categoryScore := -input.CategoryRank * 100
	agingBonus := int64(now.Sub(input.QueuedSince) / AgingInterval)
	return triggerScore + categoryScore + input.Modifiers + agingBonus
}

// ClassifyTrigger decides new_release or gap from a release date; user_request and upgrade come from why the caller searches. A nil or future date is new_release, as AvailabilityWindow (ADR-23) gates the search.
func ClassifyTrigger(releaseDate *time.Time, now time.Time, newReleaseDays int) Trigger {
	if releaseDate == nil || releaseDate.After(now) {
		return TriggerNewRelease
	}
	if now.Sub(*releaseDate) <= time.Duration(newReleaseDays)*24*time.Hour {
		return TriggerNewRelease
	}
	return TriggerGap
}

// ParseReleaseDate reads a work_date value at day precision only; a shorter value parses as unknown rather than guessed.
func ParseReleaseDate(raw string) (parsed time.Time, ok bool) {
	parsed, err := time.Parse(time.DateOnly, raw)
	return parsed, err == nil
}

// IsFresh is ADR-55's release-date window alone, the one definition the trigger, Fresh filter and dashboard read; unlike ClassifyTrigger a nil date is never fresh, since showing an item as freshly released needs a real date.
func IsFresh(releasedAt *time.Time, now time.Time, newReleaseDays int) bool {
	if releasedAt == nil {
		return false
	}
	return ClassifyTrigger(releasedAt, now, newReleaseDays) == TriggerNewRelease
}
