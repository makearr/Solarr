package wikidata_test

import (
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/metadata/wikidata"
)

type labelTransport struct{ queries []string }

func (l *labelTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	query := request.URL.Query().Get("query")
	l.queries = append(l.queries, query)
	body := `{"results":{"bindings":[
	  {"mbid":{"value":"11111111-2222-3333-4444-555555555555"},"logo":{"value":"http://commons.wikimedia.org/wiki/Special:FilePath/Ozel%20Records%20logo.svg"}},
	  {"mbid":{"value":"11111111-2222-3333-4444-555555555555"},"logo":{"value":"http://commons.wikimedia.org/wiki/Special:FilePath/Ozel%20old.png"}}]}}`
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
}

func TestLabelLogosAreReadInBulkByMusicBrainzLabelID(t *testing.T) {
	transport := &labelTransport{}
	fetcher := wikidata.New(wikidata.Config{WikidataClient: newTestClient(t, transport, "wikidata"), WikipediaClient: newTestClient(t, transport, "wikipedia")})
	logos, err := fetcher.LabelLogos(t.Context(), []string{"11111111-2222-3333-4444-555555555555", `x" } DROP`})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"11111111-2222-3333-4444-555555555555": "https://commons.wikimedia.org/wiki/Special:FilePath/Ozel%20Records%20logo.svg?width=500"}
	if !reflect.DeepEqual(logos, want) {
		t.Errorf("logos = %v, want %v", logos, want)
	}
	if len(transport.queries) != 1 || strings.Contains(transport.queries[0], "DROP") || !strings.Contains(transport.queries[0], "wdt:P966") {
		t.Errorf("queries = %q, want one naming only the valid id", transport.queries)
	}
}
