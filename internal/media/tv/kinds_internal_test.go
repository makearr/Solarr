package tv

import "testing"

func TestSeriesTypeOfKind(t *testing.T) {
	got := map[string]SeriesType{}
	for _, kind := range []string{KindSeries, KindAnime, KindDaily, KindKids, ""} {
		got[kind] = seriesTypeOfKind(kind)
	}
	want := map[string]SeriesType{KindSeries: SeriesStandard, KindAnime: SeriesAnime, KindDaily: SeriesDaily, KindKids: SeriesStandard, "": SeriesStandard}
	for kind, series := range want {
		if got[kind] != series {
			t.Errorf("kind %q gives %q, want %q", kind, got[kind], series)
		}
	}
}

func TestLibraryKindsAreUniqueAndGenreKindsSuggestThemselves(t *testing.T) {
	seen := map[string]bool{}
	for _, kind := range libraryKinds {
		if seen[kind.Key] {
			t.Errorf("kind %q listed twice", kind.Key)
		}
		seen[kind.Key] = true
	}
	for _, key := range []string{KindSports, KindNews, KindGameShow, KindTalkShow} {
		if !seen[key] {
			t.Errorf("kind %q missing", key)
		}
	}
}
