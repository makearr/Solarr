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
