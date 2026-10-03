package tv_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/media/tv"
)

func TestDetectTypeReadsAnimeAndDailyFromWhatProvidersSay(t *testing.T) {
	cases := []struct {
		name   string
		series tv.Series
		want   tv.SeriesType
	}{
		{"a Japanese series AniList lists", tv.Series{OriginalLanguage: "jpn", IDs: tv.IDs{AniList: []int64{1}}}, tv.SeriesAnime},
		{"TMDB's two-letter language", tv.Series{OriginalLanguage: "ja", IDs: tv.IDs{MAL: []int64{1}}}, tv.SeriesAnime},
		{"AniList but not Japanese", tv.Series{OriginalLanguage: "eng", IDs: tv.IDs{AniList: []int64{1}}}, tv.SeriesStandard},
		{"Japanese but not listed by an anime site", tv.Series{OriginalLanguage: "jpn"}, tv.SeriesStandard},
		{"a talk show", tv.Series{Genres: []string{"Comedy", "Talk Show"}}, tv.SeriesDaily},
		{"a drama", tv.Series{Genres: []string{"Drama"}}, tv.SeriesStandard},
	}
	for _, c := range cases {
		if got := tv.DetectType(c.series); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}
