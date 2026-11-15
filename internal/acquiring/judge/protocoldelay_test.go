package judge_test

import (
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/identify/model"
)

// A delay profile holds the other protocol's accepted releases back, then lets them through; it never discards one.
func TestProtocolDelay_HoldsTheOtherProtocolUntilTheWindowEnds(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	find := func(impl string, posted time.Time, outcome model.Outcome) judge.Find {
		return judge.Find{Conn: judge.Conn{Impl: impl}, Raw: indexer.RawItem{PublishedAt: posted}, Decision: model.Decision{Outcome: outcome}}
	}
	finds := []judge.Find{
		find("newznab", now.Add(-10*time.Minute), model.OutcomeAccept), // preferred protocol: untouched
		find("torznab", now.Add(-10*time.Minute), model.OutcomeAccept), // other, inside the window: held
		find("torznab", now.Add(-90*time.Minute), model.OutcomeAccept), // other, past the window: taken
		find("torznab", time.Time{}, model.OutcomeAccept),              // no posting time: waits the whole window
		find("torznab", now.Add(-10*time.Minute), model.OutcomeReject), // already refused: left as it is
	}
	judge.ProtocolDelay{Preferred: "usenet", Window: time.Hour, Now: now}.Apply(finds)

	got := []model.Outcome{finds[0].Decision.Outcome, finds[1].Decision.Outcome, finds[2].Decision.Outcome, finds[3].Decision.Outcome, finds[4].Decision.Outcome}
	want := []model.Outcome{model.OutcomeAccept, model.OutcomeDefer, model.OutcomeAccept, model.OutcomeDefer, model.OutcomeReject}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("outcomes = %v, want %v", got, want)
		}
	}
	deferral := finds[1].Decision.Deferrals[0]
	if deferral.Reason != "protocol-delay" || !deferral.RetryAfter.Equal(now.Add(50*time.Minute)) {
		t.Errorf("deferral = %+v, want protocol-delay retrying 50 minutes on", deferral)
	}

	none := []judge.Find{find("torznab", now, model.OutcomeAccept)}
	judge.ProtocolDelay{Preferred: "", Window: time.Hour, Now: now}.Apply(none)
	judge.ProtocolDelay{Preferred: "usenet", Window: 0, Now: now}.Apply(none)
	if none[0].Decision.Outcome != model.OutcomeAccept {
		t.Errorf("no preference or no window must delay nothing, got %v", none[0].Decision.Outcome)
	}
}

// A release with no date waits its window from the first time it was seen, not from every search (JUDGE-1).
func TestProtocolDelay_AnUndatedReleaseWaitsFromItsFirstSighting(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	undated := func(guid string) judge.Find {
		return judge.Find{Conn: judge.Conn{ID: 7, Impl: "torznab"}, Raw: indexer.RawItem{GUID: guid}, Decision: model.Decision{Outcome: model.OutcomeAccept}}
	}
	var asked []string
	delay := judge.ProtocolDelay{
		Preferred: "usenet", Window: 2 * time.Hour, Now: now,
		FirstSeen: func(_ int64, guids []string) map[string]time.Time {
			asked = append(asked, guids...)
			return map[string]time.Time{"old": now.Add(-3 * time.Hour), "recent": now.Add(-30 * time.Minute)}
		},
	}
	finds := []judge.Find{undated("old"), undated("recent"), undated("never-seen")}
	delay.Apply(finds)
	got := []model.Outcome{finds[0].Decision.Outcome, finds[1].Decision.Outcome, finds[2].Decision.Outcome}
	want := []model.Outcome{model.OutcomeAccept, model.OutcomeDefer, model.OutcomeDefer}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("outcomes = %v, want %v", got, want)
		}
	}
	if retry := finds[1].Decision.Deferrals[0].RetryAfter; !retry.Equal(now.Add(90 * time.Minute)) {
		t.Errorf("recent release retries at %v, want 90 minutes on (first seen 30 minutes ago + 2 h)", retry)
	}
	if len(asked) != 3 {
		t.Errorf("first-seen lookups = %v, want one batch of the three undated guids", asked)
	}
}
