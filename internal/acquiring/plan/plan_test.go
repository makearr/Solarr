package plan

import (
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/identify/priority"
)

var (
	testNow    = time.Date(2026, time.September, 29, 12, 0, 0, 0, time.UTC)
	testPolicy = Policy{Weights: priority.DefaultWeights(), NewReleaseDays: 7}
)

func missing() Item {
	return Item{ID: 7, Monitored: true, State: "MISSING", Searchable: true, ReleaseDate: "2019-05-03", CategoryRank: 1}
}

func TestDecideMissingGapIsEligibleNow(t *testing.T) {
	got := Decide(missing(), testPolicy, testNow)
	release := time.Date(2019, time.May, 3, 0, 0, 0, 0, time.UTC).Unix()
	want := Entry{
		ItemID: 7, Class: ClassMissing, Priority: 20*1000 - 100, ReleaseDate: &release,
		EligibleAfter: release + int64(releaseGrace/time.Second), QueuedSince: testNow.Unix(),
	}
	assertEntry(t, got, want)
}

func TestDecideNeverSearchesWhatIsNotWanted(t *testing.T) {
	cases := map[string]func(*Item){
		"unmonitored":  func(item *Item) { item.Monitored = false },
		"complete":     func(item *Item) { item.State = "COMPLETE" },
		"upgradable":   func(item *Item) { item.State = "UPGRADABLE" },
		"track itself": func(item *Item) { item.Searchable = false },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			item := missing()
			item.QueuedSince = 100
			change(&item)
			got := Decide(item, testPolicy, testNow)
			release := time.Date(2019, time.May, 3, 0, 0, 0, 0, time.UTC).Unix()
			assertEntry(t, got, Entry{ItemID: 7, Class: ClassMissing, ReleaseDate: &release, EligibleAfter: Never, QueuedSince: 100})
		})
	}
}

func TestDecideWaitsForAFutureRelease(t *testing.T) {
	item := missing()
	item.ReleaseDate = "2026-10-09"
	got := Decide(item, testPolicy, testNow)
	release := time.Date(2026, time.October, 9, 0, 0, 0, 0, time.UTC).Unix()
	want := Entry{
		ItemID: 7, Class: ClassMissing, Priority: 30*1000 - 100, ReleaseDate: &release,
		EligibleAfter: release + 12*3600, QueuedSince: testNow.Unix(),
	}
	assertEntry(t, got, want)
}

func TestDecideAgesFromWhenItWasQueued(t *testing.T) {
	item := missing()
	item.ReleaseDate = ""
	item.QueuedSince = testNow.Add(-5 * time.Hour).Unix()
	got := Decide(item, testPolicy, testNow)
	want := Entry{ItemID: 7, Class: ClassMissing, Priority: 30*1000 - 100 + 5, EligibleAfter: 0, QueuedSince: item.QueuedSince}
	assertEntry(t, got, want)
}

func TestDecideBacksOffAfterSearches(t *testing.T) {
	item := missing()
	item.LastSearchedAt = testNow.Unix()
	item.SearchCount = 2
	got := Decide(item, testPolicy, testNow)
	if want := testNow.Add(12 * time.Hour).Unix(); got.EligibleAfter != want {
		t.Fatalf("EligibleAfter = %d, want %d", got.EligibleAfter, want)
	}
}

func TestRetryAfterDoublesToAWeek(t *testing.T) {
	const last = int64(1_000_000)
	for count, want := range map[int64]time.Duration{
		0: -1, 1: 6 * time.Hour, 2: 12 * time.Hour, 3: 24 * time.Hour, 5: 96 * time.Hour,
		6: 7 * 24 * time.Hour, 60: 7 * 24 * time.Hour,
	} {
		got := RetryAfter(last, count)
		if want < 0 {
			if got != 0 {
				t.Errorf("RetryAfter(%d) = %d, want 0", count, got)
			}
			continue
		}
		if got != last+int64(want/time.Second) {
			t.Errorf("RetryAfter(%d) = %s after, want %s", count, time.Duration(got-last)*time.Second, want)
		}
	}
}

func TestEarliestReleaseReadsEveryPrecision(t *testing.T) {
	for raw, want := range map[string]time.Time{
		"2024-03-01": time.Date(2024, time.March, 1, 0, 0, 0, 0, time.UTC),
		"2024-03":    time.Date(2024, time.March, 1, 0, 0, 0, 0, time.UTC),
		"2024":       time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC),
	} {
		got, ok := EarliestRelease(raw)
		if !ok || !got.Equal(want) {
			t.Errorf("EarliestRelease(%q) = %v, %v; want %v", raw, got, ok, want)
		}
	}
	for _, raw := range []string{"", "soon", "20x4"} {
		if _, ok := EarliestRelease(raw); ok {
			t.Errorf("EarliestRelease(%q) parsed", raw)
		}
	}
}

func assertEntry(t *testing.T, got, want Entry) {
	t.Helper()
	gotRelease, wantRelease := int64(-1), int64(-1)
	if got.ReleaseDate != nil {
		gotRelease = *got.ReleaseDate
	}
	if want.ReleaseDate != nil {
		wantRelease = *want.ReleaseDate
	}
	got.ReleaseDate, want.ReleaseDate = nil, nil
	if got != want || gotRelease != wantRelease {
		t.Fatalf("Decide = %+v (release %d), want %+v (release %d)", got, gotRelease, want, wantRelease)
	}
}
