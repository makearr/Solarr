package audible_test

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/metadata/audible"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

type answer struct {
	bodies map[string]string
	asked  []string
}

func (a *answer) RoundTrip(request *http.Request) (*http.Response, error) {
	a.asked = append(a.asked, request.URL.String())
	status, body := http.StatusNotFound, `{"error":"not found"}`
	if found, ok := a.bodies[request.URL.Host+request.URL.Path]; ok {
		status, body = http.StatusOK, found
	}
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
}

func client(t *testing.T, transport http.RoundTripper, region string) audible.Client {
	t.Helper()
	fake := clock.NewFakeAtEpoch()
	http := httpx.NewClient(fake, httpx.NewGuard(nil, nil, nil),
		httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{RequestsPerMinute: 6000}}, httpx.NoJitter),
		httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute}),
		httpx.ClientConfig{Provider: "audible", UserAgent: "test", Transport: transport})
	return audible.New(http, http, func() string { return region })
}

func TestBookReadsNarratorsSeriesAndRuntime(t *testing.T) {
	transport := &answer{bodies: map[string]string{"api.audnex.us/books/B000000001": `{"asin":"B000000001","title":"The Lantern Keeper",
		"authors":[{"asin":"B00AUTHOR1","name":"Mara Quell"}],"narrators":[{"name":"Tobin Reyes"}],"publisherName":"Northlight Audio",
		"summary":"<p>A keeper of lanterns.</p>","releaseDate":"2015-03-02T00:00:00.000Z","runtimeLengthMin":612,"formatType":"unabridged",
		"seriesPrimary":{"asin":"B0SERIES01","name":"The Lantern Cycle","position":"1"},"language":"english",
		"genres":[{"asin":"1","name":"Fantasy","type":"genre"},{"asin":"2","name":"Epic","type":"tag"}]}`}}
	book, err := client(t, transport, "uk").Book(t.Context(), "b000000001")
	if err != nil {
		t.Fatal(err)
	}
	if book.Narrators[0].Name != "Tobin Reyes" || book.SeriesPrimary.Position != "1" || book.RuntimeLengthMin != 612 || book.Year() != 2015 || book.Abridged() {
		t.Errorf("book = %+v", book)
	}
	if !strings.Contains(transport.asked[0], "region=uk") {
		t.Errorf("asked %s", transport.asked[0])
	}
}

func TestSearchAsksTheRegionsStoreAndTakesTheLargestCover(t *testing.T) {
	transport := &answer{bodies: map[string]string{"api.audible.de/1.0/catalog/products": `{"products":[{"asin":"B000000001","title":"The Lantern Keeper",
		"authors":[{"name":"Mara Quell"}],"release_date":"2015-03-02","runtime_length_min":612,
		"product_images":{"500":"https://m.media-amazon.com/500.jpg","1024":"https://m.media-amazon.com/1024.jpg"},
		"series":[{"asin":"B0SERIES01","title":"The Lantern Cycle","sequence":"1"}]}]}`}}
	found, err := client(t, transport, "de").Search(t.Context(), "The Lantern Keeper", "Mara Quell", 5)
	if err != nil || len(found) != 1 {
		t.Fatalf("found %+v, %v", found, err)
	}
	if found[0].Cover() != "https://m.media-amazon.com/1024.jpg" || found[0].Year() != 2015 || found[0].Series[0].Sequence != "1" {
		t.Errorf("product = %+v", found[0])
	}
	if asked := transport.asked[0]; !strings.Contains(asked, "author=Mara+Quell") || !strings.Contains(asked, "num_results=5") {
		t.Errorf("asked %s", asked)
	}
}

func TestAnUnknownRegionIsTheUSStoreAndAMalformedASINIsNotAsked(t *testing.T) {
	transport := &answer{}
	if _, err := client(t, transport, "xx").Search(t.Context(), "Anything", "", 0); err == nil || !errors.Is(err, audible.ErrNotFound) {
		t.Errorf("err = %v", err)
	}
	if !strings.HasPrefix(transport.asked[0], "https://api.audible.com/") {
		t.Errorf("asked %s", transport.asked[0])
	}
	if _, err := client(t, transport, "").Book(t.Context(), "../x"); !errors.Is(err, audible.ErrNotFound) || len(transport.asked) != 1 {
		t.Errorf("a malformed ASIN was asked: %v", transport.asked)
	}
}
