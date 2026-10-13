package library

import "time"

// AvailabilityUnannounced is the available_basis of a part that is coming but has no date yet.
const AvailabilityUnannounced = "unannounced"

// availabilityGrace is how long after its time a part is waited for, by how sure that time is (section 19.2).
func availabilityGrace(confidence string) time.Duration {
	switch confidence {
	case "high":
		return 2 * time.Hour
	case "medium":
		return 12 * time.Hour
	default:
		return 36 * time.Hour
	}
}

// PartOut reports whether a part can be had by now: past its time and the grace of its confidence, past the whole
// day of a date without a time, and always for a part that never had either unless it is announced as coming.
func PartOut(availableAt int64, confidence, date, basis string, now time.Time) bool {
	switch {
	case availableAt > 0:
		return !time.Unix(availableAt, 0).Add(availabilityGrace(confidence)).After(now)
	case date != "":
		day, err := time.Parse("2006-01-02", date)
		if err != nil {
			// A year or a month alone: out once that period has begun.
			return date <= now.UTC().Format("2006-01-02")
		}
		return !day.Add(availabilityGrace("")).After(now)
	default:
		return basis != AvailabilityUnannounced
	}
}
