package tv

import (
	"reflect"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/db/library"
)

func missingOf(numbers ...int) map[int]library.SeriesEpisode {
	missing := map[int]library.SeriesEpisode{}
	for _, number := range numbers {
		missing[number] = library.SeriesEpisode{Season: 1, Number: number}
	}
	return missing
}

func idEpisode(number string) judge.Query {
	return judge.Query{Function: "tvsearch", Extra: map[string]string{"tvdbid": "7", "season": "1", "ep": number}, Needs: []string{"tvdbid", "season", "ep"}}
}

var idSeason = judge.Query{Function: "tvsearch", Extra: map[string]string{"tvdbid": "7", "season": "1"}, Needs: []string{"tvdbid", "season"}}

func text(words string) judge.Query { return judge.Query{Function: "search", Text: words} }

// SRCH-4: a season missing a few episodes asks for each, and keeps the season forms for last.
func TestQueriesAskForEachMissingEpisodeWhenFewAreMissing(t *testing.T) {
	w := wanted{season: 1, title: "Example Show", tvdb: "7", allCount: 10, missing: missingOf(3, 4, 9)}
	want := [][]judge.Query{
		{idEpisode("3"), idEpisode("4"), idEpisode("9")},
		{text("Example Show S01E03"), text("Example Show S01E04"), text("Example Show S01E09")},
		{idSeason},
		{text("Example Show S01")},
		{text("Example Show Season 1")},
	}
	if got := w.queries(); !reflect.DeepEqual(got, want) {
		t.Errorf("queries =\n%+v\nwant\n%+v", got, want)
	}
}

func TestQueriesAskForTheSeasonWhenMostOrAllAreMissing(t *testing.T) {
	season := [][]judge.Query{{idSeason}, {text("Example Show S01")}, {text("Example Show Season 1")}}
	for name, missing := range map[string]map[int]library.SeriesEpisode{
		"all":  missingOf(1, 2, 3, 4, 5, 6, 7, 8, 9, 10),
		"half": missingOf(1, 2, 3, 4, 5, 6),
	} {
		w := wanted{season: 1, title: "Example Show", tvdb: "7", allCount: 10, missing: missing}
		if got := w.queries(); !reflect.DeepEqual(got, season) {
			t.Errorf("%s missing: queries =\n%+v\nwant\n%+v", name, got, season)
		}
	}
}

func TestQueriesAskForTheLowestFiveEpisodes(t *testing.T) {
	w := wanted{season: 1, title: "Example Show", tvdb: "7", allCount: 40, missing: missingOf(30, 2, 9, 4, 6, 7, 20)}
	if got := w.queries()[0]; len(got) != 5 || !reflect.DeepEqual(got[4], idEpisode("9")) {
		t.Errorf("id tier = %+v, want episodes 2, 4, 6, 7, 9", got)
	}
}

// SRCH-2: a daily series is asked for by air date, newest first, before the numbered forms.
func TestQueriesOfADailySeriesCarryTheAirDate(t *testing.T) {
	w := wanted{
		season: 2024, title: "Example Late Show", tvdb: "7", daily: true, allCount: 200,
		missing: map[int]library.SeriesEpisode{
			86: {Season: 2024, Number: 86, Date: "2024-05-01"}, 87: {Season: 2024, Number: 87, Date: "2024-05-02"},
		},
	}
	got := w.queries()
	want := [][]judge.Query{
		{
			{Function: "tvsearch", Extra: map[string]string{"tvdbid": "7", "season": "2024", "ep": "05/02"}, Needs: []string{"tvdbid", "season", "ep"}},
			{Function: "tvsearch", Extra: map[string]string{"tvdbid": "7", "season": "2024", "ep": "05/01"}, Needs: []string{"tvdbid", "season", "ep"}},
		},
		{text("Example Late Show 2024 05 02"), text("Example Late Show 2024 05 01")},
	}
	if len(got) < len(want) || !reflect.DeepEqual(got[:len(want)], want) {
		t.Errorf("queries start with\n%+v\nwant\n%+v", got, want)
	}
}

func TestAirDateCoversADoubleBillAndAOneDayDrift(t *testing.T) {
	w := wanted{season: 2024, missing: map[int]library.SeriesEpisode{
		1: {Date: "2024-05-01"}, 2: {Date: "2024-05-01"}, 3: {Date: "2024-05-03"},
	}}
	if got := w.airedOn("2024-05-01"); !reflect.DeepEqual(got, []int{1, 2}) {
		t.Errorf("double bill = %v, want [1 2]", got)
	}
	if got := w.airedOn("2024-05-04"); !reflect.DeepEqual(got, []int{3}) {
		t.Errorf("one day late = %v, want [3]", got)
	}
	if got := w.airedOn("2024-05-02"); got != nil {
		t.Errorf("two candidates a day away = %v, want none", got)
	}
}

// TRACK-1 step 4: an episode the grabbed release was not judged to hold is refused; an unnamed expectation allows anything.
func TestNotInGrabbedReleaseRefusesOtherEpisodes(t *testing.T) {
	found := []library.ResolvedPart{{MediaRef: MediaRef(1, 5)}}
	if rejection := notInGrabbedRelease([]string{MediaRef(1, 5), MediaRef(1, 6)}, found); rejection != nil {
		t.Errorf("an episode of the release was refused: %+v", rejection)
	}
	if rejection := notInGrabbedRelease([]string{MediaRef(1, 6)}, found); rejection == nil {
		t.Error("an episode of another release was accepted")
	}
	if rejection := notInGrabbedRelease([]string{MediaRef(2, 5)}, found); rejection == nil {
		t.Error("the same number in another season was accepted")
	}
	if rejection := notInGrabbedRelease(nil, found); rejection != nil {
		t.Errorf("no expectation refused %+v", rejection)
	}
}

// RES-2: a folder's series is remembered for one run, not for the life of the process.
func TestFolderMemoryForgetsAnOldEntry(t *testing.T) {
	var memory FolderMemory
	start := time.Unix(1_000_000, 0)
	memory.remember("3|show", 7, start)
	if id, known := memory.recall("3|show", start.Add(time.Minute)); !known || id != 7 {
		t.Errorf("a fresh entry = %d, %v, want 7", id, known)
	}
	if _, known := memory.recall("3|show", start.Add(time.Hour)); known {
		t.Error("an hour-old entry was still remembered")
	}
}
