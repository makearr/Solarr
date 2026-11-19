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
	if want := []tv.AltTitle{{Title: "Pilot (tvmaze)"}}; !reflect.DeepEqual(pilot.AltTitles, want) {
		t.Errorf("pilot alt titles = %v, want the other source's title %v", pilot.AltTitles, want)
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

func episodeOf(season, number int, title, date string, id int64, confidence tv.Confidence) tv.Episode {
	return tv.Episode{Season: season, Number: number, Title: title, AirDate: date, AirConfidence: confidence, IDs: map[string]int64{"p": id}}
}

func TestMergeTakesAnEpisodesDetailsFromTheOneAiredTheSameDayNotFromTheOneUnderItsNumber(t *testing.T) {
	// The second source counts a double-length premiere as two episodes, so every later number is shifted by one.
	first := tv.Series{Episodes: []tv.Episode{
		episodeOf(1, 1, "Premiere", "2025-01-05", 1, tv.ConfidenceMedium),
		episodeOf(1, 2, "Second Night", "2025-01-12", 2, tv.ConfidenceMedium),
	}}
	second := tv.Series{Episodes: []tv.Episode{
		episodeOf(1, 1, "Premiere, Part 1", "2025-01-05", 11, tv.ConfidenceHigh),
		episodeOf(1, 2, "Premiere, Part 2", "2025-01-05", 12, tv.ConfidenceHigh),
		episodeOf(1, 3, "Second Night", "2025-01-12", 13, tv.ConfidenceHigh),
	}}
	second.Episodes[2].Overview = "The second night."
	merged := tv.Merge(first, second)
	got := merged.Episodes[1]
	if got.Overview != "The second night." || got.IDs["p"] != 13 || len(got.AltTitles) != 0 {
		t.Errorf("episode 2 = %+v, want the second night's synopsis and no alternate title", got)
	}
}

func TestMergeLeavesAnEpisodeAloneWhenNoEpisodeOfTheOtherSourceAiredThatDay(t *testing.T) {
	first := tv.Series{Episodes: []tv.Episode{episodeOf(1, 1, "Pilot", "2025-01-05", 1, tv.ConfidenceMedium)}}
	second := tv.Series{Episodes: []tv.Episode{episodeOf(1, 1, "Another Episode", "2025-03-09", 21, tv.ConfidenceHigh)}}
	merged := tv.Merge(first, second)
	want := episodeOf(1, 1, "Pilot", "2025-01-05", 1, tv.ConfidenceMedium)
	if !reflect.DeepEqual(merged.Episodes, []tv.Episode{want}) {
		t.Errorf("episodes = %+v, want the basis's untouched", merged.Episodes)
	}
}

func TestMergeAppendsAnEpisodeOnlyTheOtherSourceAnnouncedAndJoinsItLater(t *testing.T) {
	first := tv.Series{Episodes: []tv.Episode{episodeOf(1, 1, "Pilot", "2025-01-05", 1, tv.ConfidenceMedium)}}
	second := tv.Series{Episodes: []tv.Episode{
		episodeOf(1, 1, "Pilot", "2025-01-05", 21, tv.ConfidenceHigh),
		episodeOf(1, 2, "Announced", "2025-01-12", 22, tv.ConfidenceHigh),
		episodeOf(2, 1, "Next Year", "2026-01-04", 23, tv.ConfidenceHigh),
		episodeOf(4, 1, "Too Far", "2027-01-04", 24, tv.ConfidenceHigh),
	}}
	merged := tv.Merge(first, second)
	var refs []tv.Key
	for _, episode := range merged.Episodes {
		refs = append(refs, tv.Key{Season: episode.Season, Number: episode.Number})
	}
	if want := []tv.Key{{Season: 1, Number: 1}, {Season: 1, Number: 2}, {Season: 2, Number: 1}}; !reflect.DeepEqual(refs, want) {
		t.Errorf("episodes = %v, want %v", refs, want)
	}
	first.Episodes = append(first.Episodes, episodeOf(1, 2, "Announced", "2025-01-12", 2, tv.ConfidenceMedium))
	merged = tv.Merge(first, second)
	if len(merged.Episodes) != 3 || merged.Episodes[1].IDs["p"] != 22 {
		t.Errorf("after the basis lists it: %+v", merged.Episodes)
	}
}
