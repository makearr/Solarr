package parsetv

import (
	"reflect"
	"testing"
)

func TestParseSonarrStyleLibraryNames(t *testing.T) {
	cases := []struct {
		path     string
		series   string
		season   int
		episodes []int
		special  bool
	}{
		{"Example Show/Season 3/Example Show - S03E07 - A Title.mp4", "Example Show", 3, []int{7}, false},
		{"Example Show/Season 3/Example Show - S03E10-E11 - Part A + Part B.mp4", "Example Show", 3, []int{10, 11}, false},
		{"Example Show/Season 3/Example Show - S03E18-E21 - One + Two + Three + Four.mp4", "Example Show", 3, []int{18, 19, 20, 21}, false},
		{"Example Show/Specials/Example Show - S00E29 - A Special.mp4", "Example Show", 0, []int{29}, false},
		{"Example Show/Season 3/Example Show - S03E07 - A Title.1.en.srt", "Example Show", 3, []int{7}, false},
		{"Example Show/Season 3/07 - A Title.mkv", "Example Show", 3, []int{7}, false},
	}
	for _, c := range cases {
		info, ok := ParsePath(c.path, corpusNow)
		if !ok {
			t.Errorf("%q: not parsed", c.path)
			continue
		}
		if info.SeriesTitle != c.series && c.series != "" && info.SeriesTitle != "" || info.SeasonNumber != c.season || !reflect.DeepEqual(info.EpisodeNumbers, c.episodes) {
			t.Errorf("%q: got %q S%d %v", c.path, info.SeriesTitle, info.SeasonNumber, info.EpisodeNumbers)
		}
	}
}

func TestParseReadsAliasesAndYear(t *testing.T) {
	info, ok := Parse("Example Show (2010) - S01E02 - A Title", corpusNow)
	if !ok || info.Year != 2010 || info.TitleWithoutYear != "Example Show" {
		t.Errorf("got %+v ok=%v", info, ok)
	}
	info, ok = Parse("Primary Title AKA Other Title S02E03 720p HDTV x264-GRP", corpusNow)
	if !ok || !reflect.DeepEqual(info.AllTitles, []string{"Primary Title", "Other Title"}) {
		t.Errorf("got %+v ok=%v", info, ok)
	}
}

func TestParseRefusesFutureAndAmbiguousDates(t *testing.T) {
	if info, ok := Parse("Daily.Show.2027.01.05.720p.HDTV.x264-GRP", corpusNow); ok {
		t.Errorf("future date parsed: %+v", info)
	}
	if info, ok := Parse("Daily.Show.05.06.2020.720p.HDTV.x264-GRP", corpusNow); ok {
		t.Errorf("ambiguous day/month parsed: %+v", info)
	}
	if info, ok := Parse("Daily.Show.2020.05.06.720p.HDTV.x264-GRP", corpusNow); !ok || info.AirDate != "2020-05-06" {
		t.Errorf("got %+v ok=%v", info, ok)
	}
}

func TestCleanTitleMatchesPunctuationAndArticles(t *testing.T) {
	if CleanTitle("Show of the Name!") != CleanTitle("Show: Name") {
		t.Errorf("clean titles differ: %q vs %q", CleanTitle("Show of the Name!"), CleanTitle("Show: Name"))
	}
	if got := CleanTitle("3%"); got != "3percent" {
		t.Errorf("percent: %q", got)
	}
}
