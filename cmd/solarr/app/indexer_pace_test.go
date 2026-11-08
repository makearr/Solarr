package app

import (
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
)

func TestIndexerPacingStaysUnderTheLimitAndSlowsOnHits(t *testing.T) {
	fake := clock.NewFakeAtEpoch()
	pacing := newIndexerPacing(fake)
	if _, set := pacing.interval(1); set {
		t.Fatal("an indexer with no limit and no hit has a pace of its own")
	}
	pacing.setLimit(1, 100)
	if got, _ := pacing.interval(1); got != 45*time.Second {
		t.Errorf("100 an hour paced at %s, want 45s (80%% of the limit)", got)
	}
	pacing.limitHit(1)
	pacing.limitHit(1)
	if got, _ := pacing.interval(1); got != 180*time.Second {
		t.Errorf("after two hits %s, want 4 x 45s", got)
	}
	fake.Advance(2 * time.Hour)
	if got, _ := pacing.interval(1); got != 45*time.Second {
		t.Errorf("two quiet hours later %s, want back to 45s", got)
	}
	pacing.limitHit(2)
	if got, _ := pacing.interval(2); got != 2*indexerBasePace {
		t.Errorf("an unlimited indexer hit once %s, want twice the base pace", got)
	}
}
