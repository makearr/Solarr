package apiserver

import (
	"strings"
	"testing"
	"time"
)

func TestRenderICS(t *testing.T) {
	now := time.Date(2026, 9, 24, 8, 0, 0, 0, time.UTC)
	rows := []CalendarRow{
		{WorkID: 1, Title: "Live; at, the \\ Hall", Artist: "Goual", PrimaryType: "Album", ReleaseDate: "2026-10-02"},
		{WorkID: 2, Title: "Only A Month", ReleaseDate: "2026-11"},
	}
	got := renderICS(rows, now)
	want := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Solarr//Calendar//EN\r\nCALSCALE:GREGORIAN\r\n" +
		"X-WR-CALNAME:Solarr\r\nBEGIN:VEVENT\r\nUID:solarr-work-1\r\nDTSTAMP:20260924T080000Z\r\n" +
		"DTSTART;VALUE=DATE:20261002\r\nDTEND;VALUE=DATE:20261003\r\n" +
		"SUMMARY:Goual - Live\\; at\\, the \\\\ Hall\r\nDESCRIPTION:Album\\, wanted\r\n" +
		"TRANSP:TRANSPARENT\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	if got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
}

// RFC 5545 3.1: no line over 75 octets, a fold never splits a UTF-8 character.
func TestFoldICS(t *testing.T) {
	long := "SUMMARY:" + strings.Repeat("Pióurazo ", 20)
	folded := foldICS(long)
	for _, line := range strings.Split(folded, "\r\n") {
		if len(line) > 75 {
			t.Errorf("line of %d octets: %q", len(line), line)
		}
		if !strings.HasPrefix(line, "SUMMARY") && !strings.HasPrefix(line, " ") {
			t.Errorf("continuation without its leading space: %q", line)
		}
	}
	if unfolded := strings.ReplaceAll(folded, "\r\n ", ""); unfolded != long {
		t.Errorf("unfolding does not give the original back:\n%q", unfolded)
	}
}
