package gcd_test

import (
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/metadata/gcd"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

type answer struct{}

func (answer) RoundTrip(request *http.Request) (*http.Response, error) {
	body := `{"detail":"Not found."}`
	switch request.URL.Path {
	case "/api/series/name/Night Harbor/", "/api/series/name/Night%20Harbor/":
		body = `{"count":2,"next":"https://www.comics.org/api/series/name/Night%20Harbor/?page=2","previous":null,"results":[
		 {"api_url":"https://www.comics.org/api/series/500/?format=json","name":"Night Harbor","country":"us","language":"en","year_began":2019,"year_ended":2021,
		  "publishing_format":"was limited series","publisher":"https://www.comics.org/api/publisher/9/?format=json",
		  "active_issues":["https://www.comics.org/api/issue/7001/?format=json","https://www.comics.org/api/issue/7002/?format=json"]}]}`
	case "/api/series/500/":
		body = `{"api_url":"https://www.comics.org/api/series/500/?format=json","name":"Night Harbor","year_began":2019,"active_issues":["https://www.comics.org/api/issue/7001/?format=json"]}`
	case "/api/issue/7001/":
		body = `{"api_url":"https://www.comics.org/api/issue/7001/?format=json","series_name":"Night Harbor (2019 series)","number":"1","title":"Dawn","key_date":"2019-06-00",
		 "on_sale_date":"2019-04-10","page_count":"32.000","price":"3.99 USD","isbn":"","barcode":"123","rating":"Teen","notes":"n","variant_of":null,
		 "series":"https://www.comics.org/api/series/500/?format=json"}`
	case "/api/publisher/9/":
		body = `{"api_url":"https://www.comics.org/api/publisher/9/?format=json","name":"Acme Press","country":"us"}`
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
}

func newClient() gcd.Client {
	fake := clock.NewFakeAtEpoch()
	return gcd.New(httpx.NewClient(fake, httpx.NewGuard(nil, nil, nil), httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{RequestsPerMinute: 6000}}, httpx.NoJitter),
		httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute}), httpx.ClientConfig{Provider: "gcd", UserAgent: "test", Transport: answer{}}))
}

func TestSearchSeriesIssueAndPublisher(t *testing.T) {
	c := newClient()
	series, more, err := c.SearchSeries(t.Context(), "Night Harbor", 1)
	want := []gcd.Series{{ID: 500, Name: "Night Harbor", Country: "us", Language: "en", YearBegan: 2019, YearEnded: 2021, IssueIDs: []int{7001, 7002}, PublisherID: 9, Format: "was limited series"}}
	if err != nil || !more || !reflect.DeepEqual(series, want) {
		t.Fatalf("series = %+v more=%v err=%v", series, more, err)
	}
	one, err := c.Series(t.Context(), 500)
	if err != nil || one.ID != 500 || len(one.IssueIDs) != 1 {
		t.Fatalf("series = %+v, %v", one, err)
	}
	issue, err := c.Issue(t.Context(), 7001)
	wantIssue := gcd.Issue{ID: 7001, SeriesID: 500, SeriesName: "Night Harbor (2019 series)", Number: "1", Title: "Dawn", KeyDate: "2019-06-00", OnSaleDate: "2019-04-10",
		Pages: 32, Price: "3.99 USD", Barcode: "123", Rating: "Teen", Notes: "n"}
	if err != nil || issue != wantIssue {
		t.Fatalf("issue = %+v, %v", issue, err)
	}
	pub, err := c.Publisher(t.Context(), 9)
	if err != nil || pub != (gcd.Publisher{ID: 9, Name: "Acme Press", Country: "us"}) {
		t.Fatalf("publisher = %+v, %v", pub, err)
	}
	if _, err := c.Issue(t.Context(), 1); !errors.Is(err, gcd.ErrNotFound) {
		t.Errorf("err = %v; want ErrNotFound", err)
	}
}
