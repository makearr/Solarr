package wikidata_test

import (
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/metadata/wikidata"
)

type seriesTransport struct{ queries []string }

func (s *seriesTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	s.queries = append(s.queries, request.URL.Query().Get("query"))
	body := `{"results":{"bindings":[
	  {"series":{"value":"http://www.wikidata.org/entity/Q10"},"seriesLabel":{"value":"Lantern Cycle"},"member":{"value":"http://www.wikidata.org/entity/Q11"},"memberLabel":{"value":"The Lantern Keeper"},"ol":{"value":"OL1001W"},"ordinal":{"value":"1"},"date":{"value":"1987-03-01T00:00:00Z"}},
	  {"series":{"value":"http://www.wikidata.org/entity/Q10"},"seriesLabel":{"value":"Lantern Cycle"},"member":{"value":"http://www.wikidata.org/entity/Q12"},"memberLabel":{"value":"The Ember Gate"},"ordinal":{"value":"2"},"date":{"value":"1989-01-01T00:00:00Z"}},
	  {"series":{"value":"http://www.wikidata.org/entity/Q10"},"seriesLabel":{"value":"Lantern Cycle"},"member":{"value":"http://www.wikidata.org/entity/Q12"},"memberLabel":{"value":"The Ember Gate"},"ol":{"value":"OL25432910M"},"ordinal":{"value":"2"},"date":{"value":"1990-01-01T00:00:00Z"}},
	  {"series":{"value":"http://www.wikidata.org/entity/Q10"},"seriesLabel":{"value":"Lantern Cycle"},"member":{"value":"http://www.wikidata.org/entity/Q11"},"memberLabel":{"value":"The Lantern Keeper"},"ol":{"value":"OL1009W"},"ordinal":{"value":"1"}},
	  {"series":{"value":"http://www.wikidata.org/entity/Q10"},"seriesLabel":{"value":"Lantern Cycle"},"member":{"value":"http://www.wikidata.org/entity/Q13"},"memberLabel":{"value":"Q13"}}]}}`
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
}

func TestBookSeriesListsEachBookOfTheSeriesAHeldBookIsPartOfOnce(t *testing.T) {
	transport := &seriesTransport{}
	fetcher := wikidata.New(wikidata.Config{WikidataClient: newTestClient(t, transport, "wikidata"), WikipediaClient: newTestClient(t, transport, "wikipedia")})
	books, err := fetcher.BookSeries(t.Context(), []string{"OL1001W", `OL1" } DROP`})
	if err != nil {
		t.Fatal(err)
	}
	want := []wikidata.SeriesBook{
		{SeriesQID: "Q10", SeriesName: "Lantern Cycle", QID: "Q11", Title: "The Lantern Keeper", OpenLibrary: []string{"OL1001W", "OL1009W"}, Ordinal: "1", Year: 1987},
		{SeriesQID: "Q10", SeriesName: "Lantern Cycle", QID: "Q12", Title: "The Ember Gate", Ordinal: "2", Year: 1989},
	}
	if !reflect.DeepEqual(books, want) {
		t.Errorf("books = %+v\nwant %+v (both work ids, no edition id, the earliest year; an unlabelled item is no book to show)", books, want)
	}
	if len(transport.queries) != 1 || strings.Contains(transport.queries[0], "DROP") {
		t.Errorf("queries = %q, want one naming only the valid id", transport.queries)
	}
}
