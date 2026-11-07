package wikidata_test

import (
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/metadata/wikidata"
)

// fixedTransport answers every query with one body and keeps the queries asked.
type fixedTransport struct {
	body    string
	queries []string
}

func (f *fixedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	f.queries = append(f.queries, request.URL.Query().Get("query"))
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(f.body)), Request: request}, nil
}

func fixedFetcher(t *testing.T, transport *fixedTransport) wikidata.Fetcher {
	t.Helper()
	return wikidata.New(wikidata.Config{WikidataClient: newTestClient(t, transport, "wikidata"), WikipediaClient: newTestClient(t, transport, "wikipedia")})
}

const (
	albumOne = "11111111-1111-1111-1111-111111111111"
	albumTwo = "22222222-2222-2222-2222-222222222222"
)

func TestAlbumFranchisesTellTheSoundtrackRouteFromTheAlbumsOwn(t *testing.T) {
	transport := &fixedTransport{body: `{"results":{"bindings":[
	  {"id":{"value":"` + albumOne + `"},"f":{"value":"http://www.wikidata.org/entity/Q500"},"fLabel":{"value":"Vexmoor Saga"},"parent":{"value":"http://www.wikidata.org/entity/Q501"},"film":{"value":"http://www.wikidata.org/entity/Q900"}},
	  {"id":{"value":"` + albumOne + `"},"f":{"value":"http://www.wikidata.org/entity/Q500"},"fLabel":{"value":"Vexmoor Saga"},"parent":{"value":"http://www.wikidata.org/entity/Q502"}},
	  {"id":{"value":"` + albumTwo + `"},"f":{"value":"http://www.wikidata.org/entity/Q600"},"fLabel":{"value":"Brightwater Sessions"}},
	  {"id":{"value":"` + albumTwo + `"},"f":{"value":"not-an-item"}}]}}`}
	found, err := fixedFetcher(t, transport).AlbumFranchises(t.Context(), []string{albumOne, albumTwo, albumOne, `x" } DROP`})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]wikidata.AlbumFranchise{
		albumOne: {{Franchise: wikidata.Franchise{QID: "Q500", Label: "Vexmoor Saga", Named: true, Parents: []string{"Q501", "Q502"}}, Soundtrack: true}},
		albumTwo: {{Franchise: wikidata.Franchise{QID: "Q600", Label: "Brightwater Sessions", Named: true}}},
	}
	if !reflect.DeepEqual(found, want) {
		t.Errorf("found = %+v\nwant %+v", found, want)
	}
	if len(transport.queries) != 1 || strings.Contains(transport.queries[0], "DROP") || strings.Count(transport.queries[0], albumOne) != 1 {
		t.Errorf("queries = %q, want one naming each valid id once", transport.queries)
	}
}

func TestMusicSeriesItemsKeepTheLowestItemOfASeries(t *testing.T) {
	transport := &fixedTransport{body: `{"results":{"bindings":[
	  {"id":{"value":"` + albumOne + `"},"s":{"value":"http://www.wikidata.org/entity/Q71"}},
	  {"id":{"value":"` + albumOne + `"},"s":{"value":"http://www.wikidata.org/entity/Q70"}}]}}`}
	found, err := fixedFetcher(t, transport).MusicSeriesItems(t.Context(), []string{albumOne})
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{albumOne: "Q70"}; !reflect.DeepEqual(found, want) {
		t.Errorf("found = %v, want %v (one item per series, the same every run)", found, want)
	}
}

func TestItemFranchisesListPartsAndTheirWholesWithoutTheItemItself(t *testing.T) {
	transport := &fixedTransport{body: `{"results":{"bindings":[
	  {"s":{"value":"http://www.wikidata.org/entity/Q10"},"f":{"value":"http://www.wikidata.org/entity/Q30"},"parent":{"value":"http://www.wikidata.org/entity/Q20"}},
	  {"s":{"value":"http://www.wikidata.org/entity/Q10"},"f":{"value":"http://www.wikidata.org/entity/Q10"}},
	  {"s":{"value":"http://www.wikidata.org/entity/Q11"},"f":{"value":"http://www.wikidata.org/entity/Q30"}}]}}`}
	found, err := fixedFetcher(t, transport).ItemFranchises(t.Context(), []string{"Q10", "Q11", "Q10", "bad"})
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string][]string{"Q10": {"Q20", "Q30"}, "Q11": {"Q30"}}; !reflect.DeepEqual(found, want) {
		t.Errorf("found = %v, want %v", found, want)
	}
	if len(transport.queries) != 1 || !strings.Contains(transport.queries[0], "wd:Q10 wd:Q11 }") {
		t.Errorf("queries = %q, want one naming the valid items once", transport.queries)
	}
}
