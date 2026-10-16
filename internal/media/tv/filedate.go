package tv

import "time"

// airInstant is the moment an episode aired, from its date: midnight UTC, the most a date alone says.
func airInstant(date string) (time.Time, bool) {
	at, err := time.Parse("2006-01-02", date)
	return at, err == nil
}
