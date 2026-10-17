package wikidata_test

import (
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/metadata/wikidata"
)

// queryTransport answers a SPARQL request by what it asks for: what ties series together, or the series of franchises.
type queryTransport struct{ requests []string }

func (q *queryTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	query := request.URL.Query().Get("query")
	q.requests = append(q.requests, query)
	body := `{"results":{"bindings":[]}}`
	switch {
	case strings.Contains(query, `VALUES ?id { "81797" }`):
		body = `{"results":{"bindings":[
		  {"id":{"value":"81797"},"f":{"value":"http://www.wikidata.org/entity/Q4321"},"fLabel":{"value":"Example Franchise"},"named":{"value":"1"}},
		  {"id":{"value":"81797"},"f":{"value":"http://www.wikidata.org/entity/Q9"},"fLabel":{"value":"Example: Spin-off"},"named":{"value":"0"},"tvdb":{"value":"100"},"start":{"value":"2009-09-22T00:00:00Z"}},
		  {"id":{"value":"81797"},"f":{"value":"http://www.wikidata.org/entity/Q77"},"fLabel":{"value":"Example: The Film"},"named":{"value":"0"}}]}}`
	case strings.Contains(query, "wd:Q4321"):
		body = `{"results":{"bindings":[
		  {"q":{"value":"http://www.wikidata.org/entity/Q4321"},"m":{"value":"http://www.wikidata.org/entity/Q1"},"mLabel":{"value":"Example"},"tvdb":{"value":"81797"},"start":{"value":"2003-09-23T00:00:00Z"}},
		  {"q":{"value":"http://www.wikidata.org/entity/Q4321"},"m":{"value":"http://www.wikidata.org/entity/Q9"},"mLabel":{"value":"Example: Spin-off"},"tvdb":{"value":"100"},"tmdb":{"value":"55"},"start":{"value":"2009-09-22T00:00:00Z"}},
		  {"q":{"value":"http://www.wikidata.org/entity/Q4321"},"m":{"value":"http://www.wikidata.org/entity/Q9"},"mLabel":{"value":"Example: Spin-off"},"tvdb":{"value":"100"}}]}}`
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
}

func TestTiesAndFranchiseMembersAreReadInBulkFromSPARQL(t *testing.T) {
	transport := &queryTransport{}
	fetcher := wikidata.New(wikidata.Config{WikidataClient: newTestClient(t, transport, "wikidata"), WikipediaClient: newTestClient(t, transport, "wikipedia")})

	ties, err := fetcher.TiesOf(t.Context(), []wikidata.SeriesRef{{TVDB: 81797}})
	if err != nil {
		t.Fatal(err)
	}
	want := wikidata.SeriesTies{
		Franchises: []wikidata.Franchise{{QID: "Q4321", Label: "Example Franchise", Named: true}},
		Related:    []wikidata.FranchiseSeries{{QID: "Q9", Label: "Example: Spin-off", TVDB: 100, Year: 2009}},
	}
	if got := ties[wikidata.SeriesRef{TVDB: 81797}]; !reflect.DeepEqual(got, want) {
		t.Errorf("ties = %+v, want %+v (a film, without a series id, is no related series)", got, want)
	}
	members, err := fetcher.FranchiseMembers(t.Context(), []string{"Q4321", "Q1 } DROP"})
	if err != nil {
		t.Fatal(err)
	}
	wantMembers := []wikidata.FranchiseSeries{
		{QID: "Q1", Label: "Example", TVDB: 81797, Year: 2003},
		{QID: "Q9", Label: "Example: Spin-off", TVDB: 100, TMDB: 55, Year: 2009},
	}
	if !reflect.DeepEqual(members["Q4321"], wantMembers) {
		t.Errorf("members = %+v, want %+v (rows of one series are one)", members["Q4321"], wantMembers)
	}
	for _, query := range transport.requests {
		if strings.Contains(query, "DROP") {
			t.Error("an id that is no item id never reaches a query")
		}
	}
}
