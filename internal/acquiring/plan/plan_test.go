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
		"track itself": func(item *Item) { item.Searchable = false },
		"above rating": func(item *Item) { item.AboveRating = true },
		"an upgrade above rating": func(item *Item) {
			item.State, item.AboveRating = "UPGRADABLE", true
		},
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
		got := RetryAfter(last, count, false)
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

func TestRetryAfterFailureStaysWithinADay(t *testing.T) {
	const last = int64(1_000_000)
	for count, want := range map[int64]time.Duration{1: 6 * time.Hour, 3: 24 * time.Hour, 6: 24 * time.Hour, 60: 24 * time.Hour} {
		if got := RetryAfter(last, count, true); got != last+int64(want/time.Second) {
			t.Errorf("RetryAfter(%d, after failure) = %s after, want %s", count, time.Duration(got-last)*time.Second, want)
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

func upgradable() Item {
	item := missing()
	item.State = "UPGRADABLE"
	item.QueuedSince = testNow.Add(-time.Hour).Unix()
	item.Upgrade = UpgradeRules{MinFileAge: 24 * time.Hour, MaxPerWindow: 3}
	return item
}

// Section 9.2's hysteresis: an upgrade waits for the file's minimum age, and for a window it has used up.
func TestDecideUpgradeWaitsForTheFileToAgeAndTheWindowToFree(t *testing.T) {
	item := upgradable()
	item.ArrivedAt = testNow.Add(-2 * time.Hour).Unix()
	got := Decide(item, testPolicy, testNow)
	if got.Class != ClassUpgrade || got.EligibleAfter != item.ArrivedAt+int64(24*time.Hour/time.Second) {
		t.Errorf("a two-hour-old file: class %q eligible %d, want upgrade after its 24 h", got.Class, got.EligibleAfter)
	}

	item.ArrivedAt = testNow.Add(-72 * time.Hour).Unix()
	if got := Decide(item, testPolicy, testNow); got.Class != ClassUpgrade || got.EligibleAfter > testNow.Unix() {
		t.Errorf("an old file: class %q eligible %d, want an upgrade already due", got.Class, got.EligibleAfter)
	}

	item.RecentImports = 3
	if got := Decide(item, testPolicy, testNow); got.EligibleAfter != testNow.Add(upgradeHold).Unix() {
		t.Errorf("three imports this week: eligible %d, want held a day", got.EligibleAfter)
	}

	item.Monitored = false
	if got := Decide(item, testPolicy, testNow); got.EligibleAfter != Never {
		t.Errorf("an unmonitored upgrade: eligible %d, want never", got.EligibleAfter)
	}
}

// ADR-55: an old album an RSS feed showed a release for ranks as a new release for 48 hours, then as a gap again.
func TestDecideAnRSSHitMakesAnOldAlbumANewReleaseForFortyEightHours(t *testing.T) {
	gap := Decide(missing(), testPolicy, testNow).Priority
	item := missing()
	item.RSSSeenAt = testNow.Add(-47 * time.Hour).Unix()
	if got := Decide(item, testPolicy, testNow).Priority; got <= gap {
		t.Errorf("seen 47h ago: priority %d, want above the gap's %d", got, gap)
	}
	item.RSSSeenAt = testNow.Add(-49 * time.Hour).Unix()
	if got := Decide(item, testPolicy, testNow).Priority; got != gap {
		t.Errorf("seen 49h ago: priority %d, want the gap's %d again", got, gap)
	}
}

// The window is the instance's setting, not a constant.
func TestDecideTheRSSWindowFollowsThePolicy(t *testing.T) {
	gap := Decide(missing(), testPolicy, testNow).Priority
	item := missing()
	item.RSSSeenAt = testNow.Add(-60 * time.Hour).Unix()
	wide := testPolicy
	wide.RSSFreshHours = 72
	if got := Decide(item, wide, testNow).Priority; got <= gap {
		t.Errorf("seen 60h ago with a 72h window: priority %d, want above the gap's %d", got, gap)
	}
	narrow := testPolicy
	narrow.RSSFreshHours = 12
	item.RSSSeenAt = testNow.Add(-13 * time.Hour).Unix()
	if got := Decide(item, narrow, testNow).Priority; got != gap {
		t.Errorf("seen 13h ago with a 12h window: priority %d, want the gap's %d", got, gap)
	}
}
