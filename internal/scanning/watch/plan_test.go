package watch

import (
	"reflect"
	"testing"
	"time"
)

func TestPlan_ShallowLevelsFirstThenTheRecentlyChanged(t *testing.T) {
	dirs := []Dir{
		{1, "", 0, 0},
		{1, "A", 1, 10}, {1, "B", 1, 90}, {1, "C", 1, 50},
		{1, "A/x", 2, 5}, {1, "B/y", 2, 99}, {1, "C/z", 2, 1},
	}
	chosen, covered := Plan(dirs, 6)
	var got []string
	for _, dir := range chosen {
		got = append(got, dir.Rel)
	}
	if want := []string{"", "B", "C", "A", "B/y", "A/x"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("chosen = %v, want %v", got, want)
	}
	if covered != 1 {
		t.Fatalf("covered depth = %d, want 1: level 2 is only partly watched", covered)
	}
	if _, covered := Plan(dirs, 100); covered != 2 {
		t.Fatalf("covered depth with room = %d, want 2", covered)
	}
	if chosen, covered := Plan(dirs, 0); len(chosen) != 0 || covered != -1 {
		t.Fatalf("no budget: chosen %d covered %d, want nothing and -1", len(chosen), covered)
	}
}

func TestRecommendedWatches_NextPowerOfTwoDoubled(t *testing.T) {
	for directories, want := range map[int]int64{0: 2, 1: 2, 5: 16, 1000: 2048, 1024: 2048, 1025: 4096} {
		if got := RecommendedWatches(directories); got != want {
			t.Errorf("RecommendedWatches(%d) = %d, want %d", directories, got, want)
		}
	}
}

func TestScanDue(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	cases := map[string]struct {
		in  Input
		due bool
		why Why
	}{
		"a person asks":                {Input{Now: now, LastScan: ago(time.Minute), Manual: true}, true, WhyManual},
		"never scanned":                {Input{Now: now}, true, WhyNeverScanned},
		"start, last scan old":         {Input{Now: now, LastScan: ago(13 * time.Hour), Startup: true, Watching: true, CoveredDepth: 3}, true, WhyStartupStale},
		"start, last scan recent":      {Input{Now: now, LastScan: ago(11 * time.Hour), Startup: true}, false, WhyStartupRecent},
		"watched, two days":            {Input{Now: now, LastScan: ago(48 * time.Hour), Watching: true, CoveredDepth: 1}, false, WhyWithinInterval},
		"watched, eight days":          {Input{Now: now, LastScan: ago(8 * 24 * time.Hour), Watching: true, CoveredDepth: 2}, true, WhyIntervalPassed},
		"watcher only on the root":     {Input{Now: now, LastScan: ago(7 * time.Hour), Watching: true, CoveredDepth: 0}, true, WhyIntervalPassed},
		"no watcher, same fingerprint": {Input{Now: now, LastScan: ago(7 * time.Hour), Fingerprint: FingerprintSame}, false, WhyUnchanged},
		"same fingerprint, a day":      {Input{Now: now, LastScan: ago(25 * time.Hour), Fingerprint: FingerprintSame}, true, WhyIntervalPassed},
		"fingerprint changed":          {Input{Now: now, LastScan: ago(time.Hour), Fingerprint: FingerprintChanged}, true, WhyChangedSize},
		"blind, five hours":            {Input{Now: now, LastScan: ago(5 * time.Hour)}, false, WhyWithinInterval},
		"blind, seven hours":           {Input{Now: now, LastScan: ago(7 * time.Hour)}, true, WhyIntervalPassed},
		"person's interval wins":       {Input{Now: now, LastScan: ago(2 * time.Hour), Watching: true, CoveredDepth: 3, Interval: time.Hour}, true, WhyIntervalPassed},
	}
	for name, c := range cases {
		if due, why := ScanDue(c.in); due != c.due || why != c.why {
			t.Errorf("%s: got %v %q, want %v %q", name, due, why, c.due, c.why)
		}
	}
}
