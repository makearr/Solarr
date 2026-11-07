package musicbrainz_test

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
)

// directSeriesBody is the shape of a release group answered with inc=series-rels, names invented.
const directSeriesBody = `{
  "id": "04e9a8bd-175f-44a1-83a0-17d5e9fbd116",
  "relations": [
    {"type": "part of", "target-type": "series", "direction": "forward", "attribute-values": {"number": "2"},
     "series": {"id": "5a0b1c2d-3e4f-4a5b-8c6d-7e8f9a0b1c2d", "name": "Vexmoor Saga Scores", "type": "Release group series"}},
    {"type": "part of", "target-type": "series", "series": {"id": "", "name": "No id"}},
    {"type": "tribute", "target-type": "series", "series": {"id": "6a0b1c2d-3e4f-4a5b-8c6d-7e8f9a0b1c2d", "name": "Not a part"}}
  ]
}`

func TestDirect_ReleaseGroupSeriesKeepsOnlyThePartOfRelations(t *testing.T) {
	transport := &fakeTransport{responses: map[string]fakeResponse{
		"https://musicbrainz.invalid/release-group/" + okComputerRG + "?fmt=json&inc=series-rels": {status: http.StatusOK, body: directSeriesBody},
	}}
	source := musicbrainz.NewDirect(newTestClient(t, transport, musicbrainz.DirectName), "https://musicbrainz.invalid")
	lookup, ok := source.(musicbrainz.SeriesLookup)
	if !ok {
		t.Fatalf("%T does not implement SeriesLookup", source)
	}
	series, err := lookup.ReleaseGroupSeries(t.Context(), okComputerRG)
	if err != nil {
		t.Fatal(err)
	}
	want := []musicbrainz.Series{{MBID: "5a0b1c2d-3e4f-4a5b-8c6d-7e8f9a0b1c2d", Name: "Vexmoor Saga Scores", Type: "Release group series"}}
	if !reflect.DeepEqual(series, want) {
		t.Errorf("series = %+v, want %+v", series, want)
	}
}

type fakeSeriesSource struct {
	fakeSource
	series      []musicbrainz.Series
	seriesCalls int
}

func (f *fakeSeriesSource) ReleaseGroupSeries(context.Context, string) ([]musicbrainz.Series, error) {
	f.seriesCalls++
	return f.series, nil
}

func TestProvider_ReleaseGroupSeriesAsksOnlyACapableSourceOnceAndSaysWhenCached(t *testing.T) {
	proxy := &fakeSource{name: "proxy"}
	direct := &fakeSeriesSource{fakeSource: fakeSource{name: "direct"}, series: []musicbrainz.Series{{MBID: "s1", Name: "Scores"}}}
	provider := newProvider(t, proxy, direct)
	first, cached, err := provider.ReleaseGroupSeries(t.Context(), musicbrainz.PriorityBackfill, "rg1")
	if err != nil || cached || !reflect.DeepEqual(first, direct.series) {
		t.Fatalf("first = %+v cached %v err %v, want the source's answer, not cached", first, cached, err)
	}
	second, cached, err := provider.ReleaseGroupSeries(t.Context(), musicbrainz.PriorityBackfill, "rg1")
	if err != nil || !cached || !reflect.DeepEqual(second, direct.series) {
		t.Errorf("second = %+v cached %v err %v, want the cached answer", second, cached, err)
	}
	if direct.seriesCalls != 1 || proxy.calls != 0 {
		t.Errorf("calls: direct %d proxy %d, want 1 and 0", direct.seriesCalls, proxy.calls)
	}
}

func TestProvider_ReleaseGroupSeriesErrsWhenNoSourceCanLookThemUp(t *testing.T) {
	provider := newProvider(t, &fakeSource{name: "proxy"})
	if _, _, err := provider.ReleaseGroupSeries(t.Context(), musicbrainz.PriorityBackfill, "rg1"); err == nil {
		t.Error("err = nil, want an error: no source can answer")
	}
}
