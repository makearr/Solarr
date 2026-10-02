package music

import (
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/identify/model"
)

// A delay profile holds the other protocol's accepted releases back, then lets them through; it never discards one.
func TestProtocolDelay_HoldsTheOtherProtocolUntilTheWindowEnds(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	find := func(impl string, posted time.Time, outcome model.Outcome) acceptedFind {
		return acceptedFind{conn: IndexerConn{Impl: impl}, raw: indexer.RawItem{PublishedAt: posted}, decision: model.Decision{Outcome: outcome}}
	}
	finds := []acceptedFind{
		find("newznab", now.Add(-10*time.Minute), model.OutcomeAccept), // preferred protocol: untouched
		find("torznab", now.Add(-10*time.Minute), model.OutcomeAccept), // other, inside the window: held
		find("torznab", now.Add(-90*time.Minute), model.OutcomeAccept), // other, past the window: taken
		find("torznab", time.Time{}, model.OutcomeAccept),              // no posting time: waits the whole window
		find("torznab", now.Add(-10*time.Minute), model.OutcomeReject), // already refused: left as it is
	}
	protocolDelay{preferred: "usenet", window: time.Hour, now: now}.apply(finds)

	got := []model.Outcome{finds[0].decision.Outcome, finds[1].decision.Outcome, finds[2].decision.Outcome, finds[3].decision.Outcome, finds[4].decision.Outcome}
	want := []model.Outcome{model.OutcomeAccept, model.OutcomeDefer, model.OutcomeAccept, model.OutcomeDefer, model.OutcomeReject}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("outcomes = %v, want %v", got, want)
		}
	}
	deferral := finds[1].decision.Deferrals[0]
	if deferral.Reason != "protocol-delay" || !deferral.RetryAfter.Equal(now.Add(50*time.Minute)) {
		t.Errorf("deferral = %+v, want protocol-delay retrying 50 minutes on", deferral)
	}

	none := []acceptedFind{find("torznab", now, model.OutcomeAccept)}
	protocolDelay{preferred: "", window: time.Hour, now: now}.apply(none)
	protocolDelay{preferred: "usenet", window: 0, now: now}.apply(none)
	if none[0].decision.Outcome != model.OutcomeAccept {
		t.Errorf("no preference or no window must delay nothing, got %v", none[0].decision.Outcome)
	}
}
