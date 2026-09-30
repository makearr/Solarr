package plan

import "time"

// Why is F2's answer to "why is nothing happening" for one wanted item.
type Why string

// The answers, in the order Explain checks them: the first that holds is the reason.
const (
	WhyNotWanted         Why = "not_wanted"
	WhyDownloading       Why = "downloading"
	WhyHeld              Why = "held"
	WhyNoIndexer         Why = "no_indexer"
	WhyOff               Why = "off"
	WhyNoRoom            Why = "no_room"
	WhyNotPlanned        Why = "not_planned"
	WhyWaitingForRelease Why = "waiting_for_release"
	WhyScheduled         Why = "scheduled"
)

// Circumstances are the facts about one item and the instance that decide its automatic search.
type Circumstances struct {
	Monitored  bool
	State      string
	Searchable bool
	InFlight   bool
	Held       bool
	Indexers   int
	PerHour    int
	// NoRoom is a measured library with no headroom (ADR-12).
	NoRoom bool
	// Planned is false until the planner has written the item's hunt entry.
	Planned bool
	// ReleaseDate is Unix seconds, 0 when unknown.
	ReleaseDate int64
}

// Explain answers why an item is or is not being searched for automatically.
func Explain(c Circumstances, now time.Time) Why {
	switch {
	case !c.Monitored || !c.Searchable || (c.State != "MISSING" && c.State != "PARTIAL"):
		return WhyNotWanted
	case c.InFlight:
		return WhyDownloading
	case c.Held:
		return WhyHeld
	case c.Indexers == 0:
		return WhyNoIndexer
	case c.PerHour <= 0:
		return WhyOff
	case c.NoRoom:
		return WhyNoRoom
	case !c.Planned:
		return WhyNotPlanned
	case c.ReleaseDate > 0 && time.Unix(c.ReleaseDate, 0).Add(releaseGrace).After(now):
		return WhyWaitingForRelease
	default:
		return WhyScheduled
	}
}
