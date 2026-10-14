package tv

import (
	"reflect"
	"testing"
	"time"
)

// A person's zone moves every dated episode to the series' local air time there; an undated one stays as it was.
func TestApplyAirTimeZoneSetsEachDatedEpisodeAtTheLocalAirTime(t *testing.T) {
	provider := time.Date(2026, 3, 10, 1, 0, 0, 0, time.UTC)
	series := Series{
		AirTime: &AirTime{Hour: 21, Minute: 30, Timezone: "America/New_York", Guessed: true},
		Episodes: []Episode{
			{Season: 1, Number: 1, AirDate: "2026-03-09", AirUTC: provider, AirConfidence: ConfidenceHigh, AirBasis: "tvmaze airstamp"},
			{Season: 1, Number: 2, AirConfidence: ConfidenceUnknown},
		},
	}
	applyAirTimeZone(&series, "Europe/Berlin")

	want := []Episode{
		{Season: 1, Number: 1, AirDate: "2026-03-09", AirUTC: time.Date(2026, 3, 9, 20, 30, 0, 0, time.UTC),
			AirConfidence: ConfidenceMedium, AirBasis: "series air time 21:30 Europe/Berlin"},
		{Season: 1, Number: 2, AirConfidence: ConfidenceUnknown},
	}
	if !reflect.DeepEqual(series.Episodes, want) {
		t.Errorf("episodes = %+v\nwant %+v", series.Episodes, want)
	}
	if *series.AirTime != (AirTime{Hour: 21, Minute: 30, Timezone: "Europe/Berlin"}) {
		t.Errorf("air time = %+v, want 21:30 in Europe/Berlin, no longer a guess", *series.AirTime)
	}
}

func TestApplyAirTimeZoneLeavesASeriesWithoutAnAirTime(t *testing.T) {
	stamp := time.Date(2026, 3, 10, 1, 0, 0, 0, time.UTC)
	series := Series{Episodes: []Episode{{AirDate: "2026-03-09", AirUTC: stamp, AirConfidence: ConfidenceHigh}}}
	applyAirTimeZone(&series, "Europe/Berlin")
	if series.Episodes[0].AirUTC != stamp || series.Episodes[0].AirConfidence != ConfidenceHigh {
		t.Errorf("episode = %+v, want the provider's instant kept", series.Episodes[0])
	}
}
