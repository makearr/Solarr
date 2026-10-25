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

// bookTransport answers a facts query for one novel, part of a numbered series, and the entity API with no articles.
type bookTransport struct{ query string }

func (b *bookTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	body := `{"entities":{}}`
	if query := request.URL.Query().Get("query"); query != "" {
		b.query = query
		body = `{"results":{"bindings":[{"id":{"value":"OL1001W"},"item":{"value":"http://www.wikidata.org/entity/Q500"},
		  "openLibraryID":{"value":"OL1001W"},"goodreadsID":{"value":"77"},"isbnID":{"value":"978-0-00-000000-2"},
		  "makers":{"value":"Mara Quell"},"first":{"value":"1987-05-01T00:00:00Z"},
		  "series":{"value":"http://www.wikidata.org/entity/Q600|The Lantern Cycle|1"}}]}}`
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
}

func TestABooksFactsCarryItsIDsAuthorsAndPlaceInItsSeries(t *testing.T) {
	transport := &bookTransport{}
	paced := clock.NewFakeAtEpoch()
	fetcher := wikidata.New(wikidata.Config{WikidataClient: newTestClient(t, transport, "wikidata"), WikipediaClient: newTestClient(t, transport, "wikipedia"), Clock: paced})
	cache := wikidata.NewCached(fetcher, paced, time.Hour)

	facts, err := cache.FactsOf(t.Context(), wikidata.FactBook, []string{"OL1001W"})
	if err != nil {
		t.Fatal(err)
	}
	book := facts["OL1001W"]
	if !reflect.DeepEqual(book.Series, &wikidata.SeriesPlace{QID: "Q600", Name: "The Lantern Cycle", Position: "1"}) ||
		!reflect.DeepEqual(book.Makers, []string{"Mara Quell"}) || book.Date != "1987-05-01" {
		t.Errorf("facts = %+v", book)
	}
	if !strings.Contains(transport.query, "wdt:P648 ?id") {
		t.Errorf("the book was not found by its Open Library id: %s", transport.query)
	}
	ids, found, err := cache.BookIDs(t.Context(), "OL1001W")
	want := wikidata.WorkIDs{QID: "Q500", OpenLibrary: "OL1001W", Goodreads: "77", ISBN: "9780000000002"}
	if err != nil || !found || !reflect.DeepEqual(ids, want) {
		t.Errorf("ids = %+v, %v, %v; want %+v", ids, found, err, want)
	}
}
