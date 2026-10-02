package tv_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/metadata/skyhook"
	"github.com/makearr/Solarr/internal/metadata/tvmaze"
)

func intp(n int) *int { return &n }

func skyhookSeries() skyhook.Series {
	return skyhook.Series{
		TVDBID: 1234, TVMazeID: 42, Title: "Example Show", FirstAired: "2020-01-05", Status: "Continuing", Runtime: 40,
		Network: "Example Net", Rating: skyhook.Rating{Count: 10, Value: "6.5"}, TimeOfDay: &skyhook.TimeOfDay{Hours: 22},
		Episodes: []skyhook.Episode{
			{TVDBID: 1, SeasonNumber: 0, EpisodeNumber: 1, Title: "Recap", AirDate: "2019-12-31"},
			{TVDBID: 2, SeasonNumber: 1, EpisodeNumber: 1, AbsoluteEpisodeNumber: intp(1), Title: "Pilot", AirDate: "2020-01-05", AirDateUTC: "2020-01-06T02:00:00Z"},
			{TVDBID: 3, SeasonNumber: 1, EpisodeNumber: 2, Title: "Second", AirDate: "2020-01-12"},
		},
	}
}

func tvmazeSeries() tvmaze.Show {
	tvdb := int64(1234)
	return tvmaze.Show{
		ID: 42, Name: "Example Show", Status: "Running", Premiered: "2020-01-05", Summary: "<p>Plot <b>here</b>.</p>",
		Externals: tvmaze.Externals{TheTVDB: &tvdb, IMDb: "tt0000042"}, Schedule: tvmaze.Schedule{Time: "21:00"},
		Network: &tvmaze.Network{Name: "Example Net", Country: &tvmaze.Country{Code: "US", Timezone: "America/Chicago"}},
	}
}

func TestMergeKeepsPrimaryFieldsAndTakesTheMoreTrustworthyAirTime(t *testing.T) {
	episodes := []tvmaze.Episode{
		{ID: 90, Name: "Pilot (tvmaze)", Season: 1, Number: intp(1), Type: "regular", Airdate: "2020-01-05", Airtime: "21:00", Airstamp: "2020-01-06T03:00:00+00:00"},
		{ID: 91, Name: "Second", Season: 1, Number: intp(2), Type: "regular", Airdate: "2020-01-12", Airtime: "21:00", Airstamp: "2020-01-13T03:00:00+00:00"},
		{ID: 92, Name: "Unnumbered extra", Season: 1, Type: "insignificant_special", Airdate: "2020-01-01"},
	}
	merged := tv.Merge(tv.FromSkyhook(skyhookSeries()), tv.FromTVmaze(tvmazeSeries(), episodes, []string{"Alt Name"}, nil))

	if merged.Title != "Example Show" || merged.Status != tv.StatusContinuing || merged.IDs.TVDB != 1234 || merged.IDs.TVmaze != 42 || merged.IDs.IMDb != "tt0000042" {
		t.Errorf("series = %+v", merged)
	}
	if !reflect.DeepEqual(merged.Sources, []string{"skyhook", "tvmaze"}) || !reflect.DeepEqual(merged.Aliases, []string{"Alt Name"}) {
		t.Errorf("sources %v aliases %v", merged.Sources, merged.Aliases)
	}
	if merged.AirTime == nil || merged.AirTime.Timezone != "America/Chicago" || merged.AirTime.Hour != 21 {
		t.Errorf("air time = %+v, want TVmaze's zoned time over TVDB's bare clock", merged.AirTime)
	}
	if merged.Overview != "Plot here." {
		t.Errorf("overview = %q", merged.Overview)
	}
	if len(merged.Episodes) != 3 {
		t.Fatalf("episodes = %d, want TVDB's 3 (the unnumbered extra does not map)", len(merged.Episodes))
	}
	pilot := merged.Episodes[1]
	wantPilot := time.Date(2020, 1, 6, 3, 0, 0, 0, time.UTC)
	if pilot.Title != "Pilot" || !pilot.AirUTC.Equal(wantPilot) || pilot.AirConfidence != tv.ConfidenceHigh || pilot.Absolute != 1 || pilot.IDs["tvdb"] != 2 || pilot.IDs["tvmaze"] != 90 {
		t.Errorf("pilot = %+v", pilot)
	}
	second := merged.Episodes[2]
	if second.AirConfidence != tv.ConfidenceHigh || second.AirUTC.IsZero() {
		t.Errorf("an episode TVDB only dated gets TVmaze's instant: %+v", second)
	}
	if !merged.Episodes[0].Special {
		t.Error("season 0 is a special")
	}
}

func TestTVmazeAloneSuppliesTheEpisodeList(t *testing.T) {
	episodes := []tvmaze.Episode{{ID: 90, Name: "Pilot", Season: 1, Number: intp(1), Type: "regular", Airdate: "2020-01-05"}}
	merged := tv.Merge(tv.FromTVmaze(tvmazeSeries(), episodes, nil, nil))
	if len(merged.Episodes) != 1 || merged.Episodes[0].AirConfidence != tv.ConfidenceLow {
		t.Errorf("episodes = %+v, want a date-only low-confidence episode", merged.Episodes)
	}
}
