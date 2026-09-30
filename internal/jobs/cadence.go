package jobs

import (
	"regexp"
	"strings"
)

var (
	cadenceNumber  = regexp.MustCompile(`^\d+$`)
	cadenceEvery   = regexp.MustCompile(`^\*/\d+$`)
	cadenceWeekday = regexp.MustCompile(`^[0-6]$`)
)

// SimpleCadence reports whether the Scheduler screen shows cadence as a plain trigger (Every N
// minutes/hours, daily, weekly, monthly) rather than raw cron; web/src/app/Scheduler.tsx's parseCadence.
func SimpleCadence(cadence string) bool {
	fields := strings.Fields(cadence)
	if len(fields) != 5 || fields[3] != "*" {
		return false
	}
	minute, hour, dayOfMonth, dayOfWeek := fields[0], fields[1], fields[2], fields[4]
	if minute == "*" || cadenceEvery.MatchString(minute) {
		return hour == "*" && dayOfMonth == "*" && dayOfWeek == "*"
	}
	if !cadenceNumber.MatchString(minute) {
		return false
	}
	if dayOfMonth == "*" && dayOfWeek == "*" {
		return hour == "*" || cadenceEvery.MatchString(hour) || cadenceNumber.MatchString(hour)
	}
	if !cadenceNumber.MatchString(hour) {
		return false
	}
	return (dayOfMonth == "*" && cadenceWeekday.MatchString(dayOfWeek)) ||
		(dayOfWeek == "*" && cadenceNumber.MatchString(dayOfMonth))
}
