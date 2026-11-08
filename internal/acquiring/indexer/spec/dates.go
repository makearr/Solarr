package spec

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// dotNetLayout turns a .NET date format ("yyyy-MM-dd HH:mm:ss") into Go's reference layout.
func dotNetLayout(format string) string {
	replacements := []struct{ from, to string }{
		{"yyyy", "2006"}, {"yy", "06"}, {"MMMM", "January"}, {"MMM", "Jan"}, {"MM", "01"}, {"dddd", "Monday"}, {"ddd", "Mon"},
		{"dd", "02"}, {"HH", "15"}, {"hh", "03"}, {"mm", "04"}, {"ss", "05"}, {"tt", "PM"}, {"zzz", "-07:00"}, {"zz", "-07"},
		{"fff", "000"}, {"ff", "00"}, {"M", "1"}, {"d", "2"}, {"H", "15"}, {"h", "3"}, {"m", "4"}, {"s", "5"},
	}
	var out strings.Builder
	for i := 0; i < len(format); {
		if format[i] == '\'' {
			end := strings.IndexByte(format[i+1:], '\'')
			if end < 0 {
				out.WriteString(format[i+1:])
				break
			}
			out.WriteString(format[i+1 : i+1+end])
			i += end + 2
			continue
		}
		matched := false
		for _, one := range replacements {
			if strings.HasPrefix(format[i:], one.from) {
				out.WriteString(one.to)
				i += len(one.from)
				matched = true
				break
			}
		}
		if !matched {
			out.WriteByte(format[i])
			i++
		}
	}
	return out.String()
}

// parseDotNetDate reads value by a .NET format, taking the time zone it names or UTC; a format without a year is this year.
func parseDotNetDate(value, format string, now time.Time) (time.Time, bool) {
	if format == "" {
		return time.Time{}, false
	}
	layout := dotNetLayout(format)
	parsed, err := time.Parse(layout, value)
	if err != nil {
		return time.Time{}, false
	}
	if !strings.Contains(layout, "2006") && !strings.Contains(layout, "06") {
		parsed = time.Date(now.Year(), parsed.Month(), parsed.Day(), parsed.Hour(), parsed.Minute(), parsed.Second(), 0, parsed.Location())
	}
	return parsed, true
}

var agoPart = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*(second|sec|minute|min|hour|hr|day|week|month|year|[smhdwy])s?\b`)

// parseTimeAgo reads "1 hour 5 mins ago", "2 days" and "3w" as that long before now.
func parseTimeAgo(value string, now time.Time) (time.Time, bool) {
	matches := agoPart.FindAllStringSubmatch(strings.ToLower(value), -1)
	if len(matches) == 0 {
		return time.Time{}, false
	}
	result := now
	for _, match := range matches {
		count, _ := strconv.ParseFloat(match[1], 64)
		unit := match[2]
		var each time.Duration
		switch {
		case strings.HasPrefix(unit, "s"):
			each = time.Second
		case unit == "m" || strings.HasPrefix(unit, "min"):
			each = time.Minute
		case strings.HasPrefix(unit, "h"):
			each = time.Hour
		case strings.HasPrefix(unit, "d"):
			each = 24 * time.Hour
		case strings.HasPrefix(unit, "w"):
			each = 7 * 24 * time.Hour
		case unit == "month":
			each = 30 * 24 * time.Hour
		default:
			each = 365 * 24 * time.Hour
		}
		result = result.Add(-time.Duration(count * float64(each)))
	}
	return result, true
}

var clockTime = regexp.MustCompile(`(\d{1,2}):(\d{2})(?::(\d{2}))?\s*([AaPp][Mm])?`)

// parseFuzzyTime reads "Today 10:20", "Yesterday 08:00 PM", "2 hours ago" and the common absolute formats.
func parseFuzzyTime(value string, now time.Time) (time.Time, bool) {
	text := strings.TrimSpace(value)
	lower := strings.ToLower(text)
	day := time.Time{}
	switch {
	case strings.Contains(lower, "ago"):
		return parseTimeAgo(lower, now)
	case strings.Contains(lower, "today"):
		day = now
	case strings.Contains(lower, "yesterday"):
		day = now.AddDate(0, 0, -1)
	}
	if !day.IsZero() {
		hour, minute := 0, 0
		if match := clockTime.FindStringSubmatch(text); match != nil {
			hour, _ = strconv.Atoi(match[1])
			minute, _ = strconv.Atoi(match[2])
			if pm := strings.ToLower(match[4]); pm == "pm" && hour < 12 {
				hour += 12
			} else if pm == "am" && hour == 12 {
				hour = 0
			}
		}
		return time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, time.UTC), true
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02", "02-01-2006 15:04", "Jan 2, 2006", "2 Jan 2006", "Jan 2 2006 15:04", time.RFC1123Z, time.RFC1123} {
		if parsed, err := time.Parse(layout, text); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}
