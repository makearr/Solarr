// Package plan is ADR-6's planner decision: what one desired item's hunt queue entry should be.
// Pure: the caller reads the facts and writes the answer (private/plan/04-acquisition.md §9).
package plan

import (
	"math"
	"strconv"
	"time"

	"github.com/makearr/Solarr/internal/identify/priority"
)

// Never is the eligible_after of an item nothing should search, so its search history survives.
const Never = int64(math.MaxInt64)

// Item is what the planner knows about one desired item.
type Item struct {
	ID        int64
	Monitored bool
	// State is desired_item.state: MISSING, PARTIAL, COMPLETE or UPGRADABLE.
	State string
	// Searchable is the media type's answer to whether this item is searched on its own (ADR-23).
	Searchable bool
	// ReleaseDate is work_date's street date as stored: "2024-03-01", "2024-03", "2024" or "".
	ReleaseDate    string
	CategoryRank   int64
	LastSearchedAt int64
	SearchCount    int64
	// AfterFailure caps the backoff at maxBackoffAfterFailure.
	AfterFailure bool
	// ArrivedAt is when the album's newest file arrived and RecentImports how many downloads of it were imported
	// in the upgrade window: the hysteresis of section 9.2.
	ArrivedAt, RecentImports int64
	// RSSSeenAt is when an RSS feed first showed a release for the item (ADR-55: new to this library for 48 hours).
	RSSSeenAt int64
	Upgrade   UpgradeRules
	// QueuedSince is when the item entered the hunt queue; zero when it is not in it.
	QueuedSince int64
}

// Policy is the instance's priority settings (ADR-36, ADR-55).
type Policy struct {
	Weights        priority.Weights
	NewReleaseDays int
}

// Entry is an item's hunt queue row. EligibleAfter is Never when nothing should search it.
type Entry struct {
	ItemID        int64
	Class         string
	Priority      int64
	ReleaseDate   *int64
	EligibleAfter int64
	QueuedSince   int64
}

// The classes of hunt_queue: what is missing, and what is below its cutoff (section 9.2).
const (
	ClassMissing = "missing"
	ClassUpgrade = "upgrade"
)

// UpgradeRules is section 9.2's hysteresis for one library: a file must be this old before it is upgraded, and an
// album already imported this often within UpgradeWindow waits.
type UpgradeRules struct {
	MinFileAge   time.Duration
	MaxPerWindow int64
}

// UpgradeWindow is how far back an album's imports count against MaxPerWindow.
const UpgradeWindow = 7 * 24 * time.Hour

// upgradeHold is how long an album that used up its window is left alone before it is looked at again.
const upgradeHold = 24 * time.Hour

// releaseGrace is §9.2's medium-confidence grace: a street date says the day, not the hour.
const releaseGrace = 12 * time.Hour

// Decide answers item's hunt entry at now (§9.2: only monitored MISSING and PARTIAL are searched).
func Decide(item Item, policy Policy, now time.Time) Entry {
	entry := Entry{ItemID: item.ID, Class: ClassMissing, EligibleAfter: Never, QueuedSince: item.QueuedSince}
	released, known := EarliestRelease(item.ReleaseDate)
	if known {
		unix := released.Unix()
		entry.ReleaseDate = &unix
	}
	if item.Monitored && item.Searchable && item.State == "UPGRADABLE" {
		return decideUpgrade(item, entry, policy, now)
	}
	if !item.Monitored || !item.Searchable || (item.State != "MISSING" && item.State != "PARTIAL") {
		return entry
	}
	if entry.QueuedSince == 0 {
		entry.QueuedSince = now.Unix()
	}
	var releasePtr *time.Time
	if known {
		releasePtr = &released
	}
	trigger := priority.ClassifyTrigger(releasePtr, now, policy.NewReleaseDays)
	if item.RSSSeenAt > 0 && now.Sub(time.Unix(item.RSSSeenAt, 0)) <= priority.RSSFreshHours*time.Hour {
		trigger = priority.TriggerNewRelease
	}
	entry.Priority = priority.Compute(policy.Weights, priority.Input{
		Trigger:      trigger,
		CategoryRank: item.CategoryRank,
		QueuedSince:  time.Unix(entry.QueuedSince, 0),
	}, now)
	entry.EligibleAfter = max(RetryAfter(item.LastSearchedAt, item.SearchCount, item.AfterFailure), 0)
	if known {
		entry.EligibleAfter = max(entry.EligibleAfter, released.Add(releaseGrace).Unix())
	}
	return entry
}

// decideUpgrade plans an album below its cutoff: after the file's minimum age, and not while the album has used
// up its imports for the window.
func decideUpgrade(item Item, entry Entry, policy Policy, now time.Time) Entry {
	entry.Class = ClassUpgrade
	if entry.QueuedSince == 0 {
		entry.QueuedSince = now.Unix()
	}
	entry.Priority = priority.Compute(policy.Weights, priority.Input{
		Trigger: priority.TriggerUpgrade, CategoryRank: item.CategoryRank, QueuedSince: time.Unix(entry.QueuedSince, 0),
	}, now)
	entry.EligibleAfter = max(RetryAfter(item.LastSearchedAt, item.SearchCount, item.AfterFailure), 0)
	if item.ArrivedAt > 0 {
		entry.EligibleAfter = max(entry.EligibleAfter, item.ArrivedAt+int64(item.Upgrade.MinFileAge/time.Second))
	}
	if item.Upgrade.MaxPerWindow > 0 && item.RecentImports >= item.Upgrade.MaxPerWindow {
		entry.EligibleAfter = max(entry.EligibleAfter, now.Add(upgradeHold).Unix())
	}
	return entry
}

// Backoff after the first unanswered search, doubling to maxBackoff (a week, like a weekly re-check).
const (
	firstBackoff = 6 * time.Hour
	maxBackoff   = 7 * 24 * time.Hour
	// maxBackoffAfterFailure keeps a failed download's re-search a day away at most.
	maxBackoffAfterFailure = 24 * time.Hour
)

// RetryAfter is when an item searched searchCount times, last at lastSearchedAt, may be searched again.
func RetryAfter(lastSearchedAt, searchCount int64, afterFailure bool) int64 {
	if searchCount <= 0 || lastSearchedAt <= 0 {
		return 0
	}
	delay := firstBackoff
	for range searchCount - 1 {
		if delay >= maxBackoff {
			break
		}
		delay *= 2
	}
	limit := maxBackoff
	if afterFailure {
		limit = maxBackoffAfterFailure
	}
	return lastSearchedAt + int64(min(delay, limit)/time.Second)
}

// EarliestRelease reads a street date at whatever precision it has, as the first day it could mean.
func EarliestRelease(raw string) (time.Time, bool) {
	for _, layout := range []string{time.DateOnly, "2006-01"} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed, true
		}
	}
	if len(raw) == 4 {
		if year, err := strconv.Atoi(raw); err == nil {
			return time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC), true
		}
	}
	return time.Time{}, false
}
