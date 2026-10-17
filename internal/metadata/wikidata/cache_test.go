package wikidata_test

import (
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/metadata/wikidata"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// factsTransport answers a facts query for series 100 and 200, and Wikidata's entity API with no articles.
type factsTransport struct{ queries int }

func (f *factsTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	body := `{"entities":{}}`
	if query := request.URL.Query().Get("query"); query != "" {
		f.queries++
		body = `{"results":{"bindings":[
		  {"id":{"value":"100"},"item":{"value":"http://www.wikidata.org/entity/Q100"},"tvdbID":{"value":"100"},"tmdbSeriesID":{"value":"7"},"imdbID":{"value":"tt0000100"},"genres":{"value":"crime drama television series"}},
		  {"id":{"value":"200"},"item":{"value":"http://www.wikidata.org/entity/Q200"},"tvdbID":{"value":"200"},"anilistIDs":{"value":"5|6"}}]}}`
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
}

func TestASweepWarmsTheCacheOnceAndEachLookupAfterAsksNothing(t *testing.T) {
	transport := &factsTransport{}
	paced := clock.NewFakeAtEpoch()
	fetcher := wikidata.New(wikidata.Config{WikidataClient: newTestClient(t, transport, "wikidata"), WikipediaClient: newTestClient(t, transport, "wikipedia"), Clock: paced})
	cache := wikidata.NewCached(fetcher, paced, time.Hour)

	if err := cache.Warm(t.Context(), wikidata.FactSeries, []string{"100", "200", "300"}); err != nil {
		t.Fatal(err)
	}
	ids, found, err := cache.SeriesIDs(t.Context(), wikidata.WorkIDs{TVDB: 100})
	if err != nil || !found || !reflect.DeepEqual(ids, wikidata.WorkIDs{QID: "Q100", TVDB: 100, TMDB: 7, IMDb: "tt0000100"}) {
		t.Errorf("ids = %+v, %v, %v", ids, found, err)
	}
	facts, _ := cache.FactsOf(t.Context(), wikidata.FactSeries, []string{"200", "300"})
	if len(facts) != 1 || len(facts["200"].IDs.AniList) != 2 {
		t.Errorf("facts = %+v; want 200 kept and 300 kept as unknown", facts)
	}
	if transport.queries != 1 {
		t.Errorf("queries = %d; want the one bulk read, nothing per lookup", transport.queries)
	}
	paced.Advance(2 * time.Hour)
	if _, err := cache.FactsOf(t.Context(), wikidata.FactSeries, []string{"100"}); err != nil || transport.queries != 2 {
		t.Errorf("queries = %d, %v; want what was kept too long read again", transport.queries, err)
	}
}
