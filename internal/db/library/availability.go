package library

import (
	"database/sql"
	"time"
)

// AvailabilityUnannounced is the available_basis of a part that is coming but has no date yet.
const AvailabilityUnannounced = "unannounced"

// AvailabilityGrace is how long after its time a part is waited for, by how sure that time is (section 19.2).
type AvailabilityGrace struct{ High, Medium, Low time.Duration }

// DefaultAvailabilityGrace is the wait of a part no grace was stamped on.
var DefaultAvailabilityGrace = AvailabilityGrace{High: 2 * time.Hour, Medium: 12 * time.Hour, Low: 36 * time.Hour}

// For answers the grace of a confidence; a date alone or an unknown one waits the longest.
func (g AvailabilityGrace) For(confidence string) time.Duration {
	switch confidence {
	case "high":
		return g.High
	case "medium":
		return g.Medium
	default:
		return g.Low
	}
}

// Longest is the longest of the three, for how far back a re-count must look.
func (g AvailabilityGrace) Longest() time.Duration { return max(g.High, g.Medium, g.Low) }

// PartGrace is a part's stamped grace, else the default of its confidence.
func PartGrace(stampedSeconds sql.NullInt64, confidence string) time.Duration {
	if stampedSeconds.Valid {
		return time.Duration(stampedSeconds.Int64) * time.Second
	}
	return DefaultAvailabilityGrace.For(confidence)
}

// PartOut reports whether a part can be had by now: past its time and its grace, past a date without a time and its
// grace, and always for a part that never had either unless it is announced as coming.
func PartOut(availableAt int64, grace time.Duration, date, basis string, now time.Time) bool {
	switch {
	case availableAt > 0:
		return !time.Unix(availableAt, 0).Add(grace).After(now)
	case date != "":
		day, err := time.Parse("2006-01-02", date)
		if err != nil {
			// A year or a month alone: out once that period has begun.
			return date <= now.UTC().Format("2006-01-02")
		}
		return !day.Add(grace).After(now)
	default:
		return basis != AvailabilityUnannounced
	}
}
