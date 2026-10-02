package parsetv

import (
	"reflect"
	"testing"
	"time"
)

var corpusNow = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

type corpusCase struct {
	Fixture string `json:"f"`
	Args    []any  `json:"a"`
}

func loadCases(t *testing.T, method string) []corpusCase { return readJSONL[corpusCase](t, method) }

func intArg(a any) int { return int(a.(float64)) }

func intsArg(a any) []int {
	list := a.([]any)
	out := make([]int, len(list))
	for i, v := range list {
		out[i] = intArg(v)
	}
	return out
}

func sameInts(a, b []int) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

func rangeInts(low, high int) []int {
	var out []int
	for n := low; n <= high; n++ {
		out = append(out, n)
	}
	return out
}

func TestCorpusSingleEpisode(t *testing.T) {
	for _, c := range loadCases(t, "should_parse_single_episode") {
		title := c.Args[0].(string)
		info, ok := Parse(title, corpusNow)
		if !ok {
			t.Errorf("%q: not parsed", title)
			continue
		}
		if len(info.EpisodeNumbers) != 1 || info.SeasonNumber != intArg(c.Args[2]) || info.EpisodeNumbers[0] != intArg(c.Args[3]) ||
			info.SeriesTitle != c.Args[1].(string) || len(info.AbsoluteNumbers) != 0 || info.FullSeason {
			t.Errorf("%q: got title=%q season=%d episodes=%v absolute=%v full=%v, want %q S%v E%v", title,
				info.SeriesTitle, info.SeasonNumber, info.EpisodeNumbers, info.AbsoluteNumbers, info.FullSeason, c.Args[1], c.Args[2], c.Args[3])
		}
	}
}

func TestCorpusMultiEpisode(t *testing.T) {
	for _, c := range loadCases(t, "should_parse_multiple_episodes") {
		title := c.Args[0].(string)
		info, ok := Parse(title, corpusNow)
		if !ok {
			t.Errorf("%q: not parsed", title)
			continue
		}
		if info.SeasonNumber != intArg(c.Args[2]) || !sameInts(info.EpisodeNumbers, intsArg(c.Args[3])) ||
			info.SeriesTitle != c.Args[1].(string) || len(info.AbsoluteNumbers) != 0 || info.FullSeason {
			t.Errorf("%q: got title=%q season=%d episodes=%v, want %q S%v %v", title, info.SeriesTitle, info.SeasonNumber, info.EpisodeNumbers, c.Args[1], c.Args[2], c.Args[3])
		}
	}
}

func TestCorpusAbsolute(t *testing.T) {
	for _, c := range loadCases(t, "should_parse_absolute_numbers") {
		title := c.Args[0].(string)
		info, ok := Parse(title, corpusNow)
		if !ok {
			t.Errorf("%q: not parsed", title)
			continue
		}
		wantEpisode := intArg(c.Args[4])
		gotEpisode := 0
		if len(info.EpisodeNumbers) == 1 {
			gotEpisode = info.EpisodeNumbers[0]
		}
		if len(info.AbsoluteNumbers) != 1 || info.AbsoluteNumbers[0] != intArg(c.Args[2]) || info.SeasonNumber != intArg(c.Args[3]) ||
			gotEpisode != wantEpisode || info.SeriesTitle != c.Args[1].(string) || info.FullSeason {
			t.Errorf("%q: got title=%q absolute=%v season=%d episodes=%v, want %q abs %v S%v E%v", title,
				info.SeriesTitle, info.AbsoluteNumbers, info.SeasonNumber, info.EpisodeNumbers, c.Args[1], c.Args[2], c.Args[3], c.Args[4])
		}
	}
}

func TestCorpusMultiAbsolute(t *testing.T) {
	for _, c := range loadCases(t, "should_parse_multi_episode_absolute_numbers") {
		title := c.Args[0].(string)
		info, ok := Parse(title, corpusNow)
		if !ok {
			t.Errorf("%q: not parsed", title)
			continue
		}
		if !sameInts(info.AbsoluteNumbers, rangeInts(intArg(c.Args[2]), intArg(c.Args[3]))) || info.SeriesTitle != c.Args[1].(string) || info.FullSeason {
			t.Errorf("%q: got title=%q absolute=%v, want %q %v-%v", title, info.SeriesTitle, info.AbsoluteNumbers, c.Args[1], c.Args[2], c.Args[3])
		}
	}
}

func TestCorpusDaily(t *testing.T) {
	for _, c := range loadCases(t, "should_parse_daily_episode") {
		title := c.Args[0].(string)
		info, ok := Parse(title, corpusNow)
		want := time.Date(intArg(c.Args[2]), time.Month(intArg(c.Args[3])), intArg(c.Args[4]), 0, 0, 0, 0, time.UTC).Format("2006-01-02")
		if !ok || info.SeriesTitle != c.Args[1].(string) || info.AirDate != want || len(info.EpisodeNumbers) != 0 || len(info.AbsoluteNumbers) != 0 || info.FullSeason {
			t.Errorf("%q: got %+v ok=%v, want %q %s", title, info, ok, c.Args[1], want)
		}
	}
}

func TestCorpusFullSeason(t *testing.T) {
	for _, c := range loadCases(t, "should_parse_full_season_release") {
		title := c.Args[0].(string)
		info, ok := Parse(title, corpusNow)
		if !ok || info.SeasonNumber != intArg(c.Args[2]) || info.SeriesTitle != c.Args[1].(string) || len(info.EpisodeNumbers) != 0 || !info.FullSeason {
			t.Errorf("%q: got title=%q season=%d episodes=%v full=%v ok=%v, want %q S%v", title, info.SeriesTitle, info.SeasonNumber, info.EpisodeNumbers, info.FullSeason, ok, c.Args[1], c.Args[2])
		}
	}
	for _, c := range loadCases(t, "should_parse_season_extras") {
		title := c.Args[0].(string)
		info, ok := Parse(title, corpusNow)
		if !ok || info.SeasonNumber != intArg(c.Args[2]) || info.SeriesTitle != c.Args[1].(string) || !info.FullSeason || !info.SeasonExtra {
			t.Errorf("%q: got %+v ok=%v", title, info, ok)
		}
	}
	for _, c := range loadCases(t, "should_parse_multi_season_release") {
		title := c.Args[0].(string)
		info, ok := Parse(title, corpusNow)
		if !ok || info.SeasonNumber != intArg(c.Args[2]) || info.SeriesTitle != c.Args[1].(string) || !info.FullSeason || !info.MultiSeason || info.PartialSeason {
			t.Errorf("%q: got %+v ok=%v", title, info, ok)
		}
	}
}

func TestCorpusMiniSeries(t *testing.T) {
	for _, c := range loadCases(t, "should_parse_mini_series_episode") {
		title := c.Args[0].(string)
		info, ok := Parse(title, corpusNow)
		if !ok || len(info.EpisodeNumbers) != 1 || info.SeasonNumber != 1 || info.EpisodeNumbers[0] != intArg(c.Args[2]) || info.SeriesTitle != c.Args[1].(string) {
			t.Errorf("%q: got %+v ok=%v", title, info, ok)
		}
	}
}

func TestCorpusRejectsCrap(t *testing.T) {
	for _, c := range loadCases(t, "should_not_parse_crap") {
		title := c.Args[0].(string)
		if info, ok := Parse(title, corpusNow); ok {
			t.Errorf("%q: parsed as %+v", title, info)
		}
	}
}

func TestCorpusPaths(t *testing.T) {
	for _, c := range loadCases(t, "should_parse_from_path") {
		path := c.Args[0].(string)
		info, ok := ParsePath(path, corpusNow)
		if !ok || len(info.EpisodeNumbers) != 1 || info.SeasonNumber != intArg(c.Args[1]) || info.EpisodeNumbers[0] != intArg(c.Args[2]) || len(info.AbsoluteNumbers) != 0 || info.FullSeason {
			t.Errorf("%q: got %+v ok=%v, want S%v E%v", path, info, ok, c.Args[1], c.Args[2])
		}
	}
	for _, c := range loadCases(t, "should_parse_multi_episode_from_path") {
		path := c.Args[0].(string)
		info, ok := ParsePath(path, corpusNow)
		if !ok || info.SeriesTitle != c.Args[1].(string) || info.SeasonNumber != intArg(c.Args[2]) || !sameInts(info.EpisodeNumbers, intsArg(c.Args[3])) {
			t.Errorf("%q: got %+v ok=%v", path, info, ok)
		}
	}
}

func TestCorpusReleaseGroups(t *testing.T) {
	for _, method := range []string{"should_parse_release_group", "should_parse_exception_release_group", "should_parse_anime_release_groups"} {
		for _, c := range loadCases(t, method) {
			title := c.Args[0].(string)
			want, _ := c.Args[1].(string)
			if got := ParseReleaseGroup(title); got != want {
				t.Errorf("%s %q: group %q, want %q", method, title, got, want)
			}
		}
	}
	for _, c := range loadCases(t, "should_parse_releasegroup_and_hash") {
		title := c.Args[0].(string)
		info, ok := Parse(title, corpusNow)
		if !ok || info.ReleaseGroup != c.Args[1].(string) || info.ReleaseHash != c.Args[2].(string) {
			t.Errorf("%q: group %q hash %q, want %v %v", title, info.ReleaseGroup, info.ReleaseHash, c.Args[1], c.Args[2])
		}
	}
}

func TestCorpusChineseAnime(t *testing.T) {
	for _, c := range loadCases(t, "should_parse_chinese_anime_releases") {
		title := c.Args[0].(string)
		info, ok := Parse(title, corpusNow)
		if !ok || info.ReleaseGroup != c.Args[2].(string) || len(info.AbsoluteNumbers) != 1 || info.AbsoluteNumbers[0] != intArg(c.Args[3]) || info.SeriesTitle != c.Args[1].(string) || info.FullSeason {
			t.Errorf("%q: got %+v ok=%v", title, info, ok)
		}
	}
}

func TestCorpusSeriesNames(t *testing.T) {
	for _, c := range loadCases(t, "should_parse_series_name") {
		title := c.Args[0].(string)
		got := title
		if info, ok := Parse(title, corpusNow); ok {
			got = info.SeriesTitle
		}
		if CleanTitle(got) != CleanTitle(c.Args[1].(string)) {
			t.Errorf("%q: %q, want %q", title, CleanTitle(got), CleanTitle(c.Args[1].(string)))
		}
	}
}
