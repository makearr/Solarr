package app

import (
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/db/settings"
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

// The owner's stated limit wins over the tracker's declared one (IDX-7 step 3).
func TestAllowancePerHour_StatedLimitWinsOverTheTrackersOwn(t *testing.T) {
	tracker := settings.Indexer{Impl: indexer.ImplBroadcastheNet}
	if got := allowancePerHour(tracker); got != 150 {
		t.Errorf("no stated limit: %d, want the tracker's 150", got)
	}
	tracker.SettingsJSON = `{"queries_per_hour":60}`
	if got := allowancePerHour(tracker); got != 60 {
		t.Errorf("stated 60: %d", got)
	}
}
