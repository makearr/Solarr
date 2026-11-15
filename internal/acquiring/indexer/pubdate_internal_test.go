package indexer

import (
	"testing"
	"time"
)

// Feeds write the date in several RFC 822 / ISO 8601 spellings; unreadable ones stay zero (IDX-2).
func TestParsePubDate(t *testing.T) {
	instant := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	for raw, want := range map[string]time.Time{
		"Thu, 05 Nov 2026 10:00:00 +0000":   instant,
		"Thu, 05 Nov 2026 10:00:00 GMT":     instant,
		"Thu, 5 Nov 2026 10:00:00 +0000":    instant,
		"Thu, 5 Nov 2026 10:00:00 GMT":      instant,
		"5 Nov 2026 10:00:00 +0000":         instant,
		"05 Nov 2026 10:00:00 +0000":        instant,
		"05 Nov 26 10:00 GMT":               instant,
		"Thu, 05 Nov 2026 12:00:00 +0200":   instant,
		"2026-11-05T10:00:00Z":              instant,
		"2026-11-05 10:00:00":               instant,
		"  Thu, 5 Nov 2026 10:00:00 +0000 ": instant,
		"yesterday":                         {},
		"":                                  {},
	} {
		if got := parsePubDate(raw); !got.Equal(want) {
			t.Errorf("parsePubDate(%q) = %v, want %v", raw, got, want)
		}
	}
}
