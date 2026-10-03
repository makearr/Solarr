package adapters

import (
	"context"
	"errors"
	"testing"

	"github.com/makearr/Solarr/internal/importing/arrimport"
	"github.com/makearr/Solarr/internal/media/tv"
)

type fakeEpisodes struct {
	episodes []arrimport.SonarrEpisode
	err      error
}

func (f fakeEpisodes) Episodes(context.Context, int64) ([]arrimport.SonarrEpisode, error) {
	return f.episodes, f.err
}

type recordingTV struct {
	wanted map[tv.Key]bool
}

func (*recordingTV) Add(context.Context, tv.AddRequest) (tv.AddResult, error) {
	return tv.AddResult{}, nil
}
func (r *recordingTV) SetEpisodeMonitoring(_ context.Context, _, _ int64, wanted map[tv.Key]bool) error {
	r.wanted = wanted
	return nil
}
func (*recordingTV) SetProfile(context.Context, int64, int64, *int64) error { return nil }

func TestCarryOverMonitoringGivesTheCreatedSeriesSonarrsEpisodeChoices(t *testing.T) {
	recorder := &recordingTV{}
	sync := ArrSync{TV: recorder}
	series := arrimport.SonarrSeries{ID: 7, Monitored: true}
	client := fakeEpisodes{episodes: []arrimport.SonarrEpisode{
		{SeasonNumber: 1, EpisodeNumber: 1, Monitored: true}, {SeasonNumber: 1, EpisodeNumber: 2, Monitored: false}, {SeasonNumber: 0, EpisodeNumber: 1, Monitored: false},
	}}
	if err := sync.carryOverMonitoring(t.Context(), 3, 9, series, client); err != nil {
		t.Fatal(err)
	}
	want := map[tv.Key]bool{{Season: 1, Number: 1}: true, {Season: 1, Number: 2}: false, {Season: 0, Number: 1}: false}
	if len(recorder.wanted) != 3 || recorder.wanted[tv.Key{Season: 1, Number: 1}] != want[tv.Key{Season: 1, Number: 1}] || recorder.wanted[tv.Key{Season: 1, Number: 2}] {
		t.Errorf("wanted = %v, want %v", recorder.wanted, want)
	}

	recorder.wanted = nil
	series.Monitored = false
	if err := sync.carryOverMonitoring(t.Context(), 3, 9, series, client); err != nil {
		t.Fatal(err)
	}
	for key, on := range recorder.wanted {
		if on {
			t.Errorf("%v wanted although the series is unmonitored in Sonarr", key)
		}
	}
}

func TestCarryOverMonitoringFallsBackToSeasonFlagsWhenEpisodesCannotBeRead(t *testing.T) {
	recorder := &recordingTV{}
	sync := ArrSync{TV: recorder}
	series := arrimport.SonarrSeries{ID: 7, Monitored: false}
	if err := sync.carryOverMonitoring(t.Context(), 3, 9, series, fakeEpisodes{err: errors.New("down")}); err != nil {
		t.Fatal(err)
	}
	if recorder.wanted != nil {
		t.Errorf("wanted = %v: nothing to carry over, and an unmonitored series has no season flags to apply", recorder.wanted)
	}
}
