package wikidata_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/metadata/wikidata"
)

// queryTransport answers a SPARQL request by what it asks for: the franchise of a series, or the series of one.
type queryTransport struct{ requests []string }

func (q *queryTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	query := request.URL.Query().Get("query")
	q.requests = append(q.requests, query)
	body := `{"results":{"bindings":[]}}`
	switch {
	case strings.Contains(query, `wdt:P4835 "81797"`):
		body = `{"results":{"bindings":[{"f":{"value":"http://www.wikidata.org/entity/Q4321"},"fLabel":{"value":"Example Franchise"}}]}}`
	case strings.Contains(query, "wd:Q4321"):
		body = `{"results":{"bindings":[
		  {"m":{"value":"http://www.wikidata.org/entity/Q4321"},"mLabel":{"value":"Example"},"tvdb":{"value":"81797"},"start":{"value":"2003-09-23T00:00:00Z"}},
		  {"m":{"value":"http://www.wikidata.org/entity/Q9"},"mLabel":{"value":"Example: Spin-off"},"tvdb":{"value":"100"},"tmdb":{"value":"55"},"start":{"value":"2009-09-22T00:00:00Z"}},
		  {"m":{"value":"http://www.wikidata.org/entity/Q9"},"mLabel":{"value":"Example: Spin-off"},"tvdb":{"value":"100"}}]}}`
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
}

func TestSeriesFranchiseAndItsSeriesAreReadFromSPARQL(t *testing.T) {
	transport := &queryTransport{}
	fetcher := wikidata.New(wikidata.Config{WikidataClient: newTestClient(t, transport, "wikidata"), WikipediaClient: newTestClient(t, transport, "wikipedia")})

	franchise, found, err := fetcher.SeriesFranchise(t.Context(), 81797, 0)
	if err != nil || !found || franchise.QID != "Q4321" || franchise.Label != "Example Franchise" {
		t.Fatalf("franchise = %+v, %v, %v", franchise, found, err)
	}
	if _, found, _ := fetcher.SeriesFranchise(t.Context(), 1, 0); found {
		t.Error("a series Wikidata knows no franchise of has none")
	}
	members, err := fetcher.FranchiseSeriesOf(t.Context(), "Q4321")
	if err != nil {
		t.Fatal(err)
	}
	want := []wikidata.FranchiseSeries{
		{QID: "Q4321", Label: "Example", TVDB: 81797, Year: 2003},
		{QID: "Q9", Label: "Example: Spin-off", TVDB: 100, TMDB: 55, Year: 2009},
	}
	if len(members) != 2 || members[0] != want[0] || members[1] != want[1] {
		t.Errorf("members = %+v, want %+v (rows of one series are one)", members, want)
	}
	if _, err := fetcher.FranchiseSeriesOf(t.Context(), "Q1 } DROP"); err == nil {
		t.Error("an id that is no item id is refused before it reaches a query")
	}
}
