package metron_test

import (
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/metadata/metron"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

type answer struct{}

func (answer) RoundTrip(request *http.Request) (*http.Response, error) {
	status := http.StatusOK
	var body string
	if user, pass, ok := request.BasicAuth(); !ok || user != "owner" || pass != "secret" {
		status, body = http.StatusUnauthorized, `{"detail":"Authentication credentials were not provided."}`
	} else {
		switch request.URL.Path {
		case "/api/series/":
			body = `{"count":1,"next":null,"results":[{"id":55,"name":"Night Harbor","volume":1,"year_began":2019,"year_end":2021,"series_type":{"id":3,"name":"Limited Series"},
			 "publisher":{"id":2,"name":"Acme Press"},"imprint":null,"issue_count":6,"status":"Completed","cv_id":1001,"gcd_id":500}]}`
		case "/api/series/55/":
			body = `{"id":55,"name":"Night Harbor","year_began":2019,"issue_count":6,"cv_id":1001}`
		case "/api/issue/":
			body = `{"count":2,"next":"https://metron.cloud/api/issue/?page=2","results":[{"id":900,"series":{"id":55,"name":"Night Harbor"},"number":"1","name":["Dawn"],"cover_date":"2019-06-01","store_date":"2019-04-10","page":32}]}`
		case "/api/issue/900/":
			body = `{"id":900,"series":{"id":55,"name":"Night Harbor"},"number":"1","name":["Dawn"],"cover_date":"2019-06-01","store_date":"2019-04-10","page":32,"price":"3.99",
			 "rating":{"id":2,"name":"Teen"},"desc":"d","image":"https://img.example/1.jpg","arcs":[{"id":4,"name":"Long Night"}],
			 "credits":[{"creator":"Ann Quill","role":[{"id":1,"name":"Writer"},{"id":2,"name":"Cover"}]}],"reprints":[{"id":901},{"id":902}],"cv_id":5001,"gcd_id":7001}`
		default:
			status, body = http.StatusNotFound, `{"detail":"Not found."}`
		}
	}
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
}

func newClient(user, pass string) metron.Client {
	fake := clock.NewFakeAtEpoch()
	client := httpx.NewClient(fake, httpx.NewGuard(nil, nil, nil), httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{RequestsPerMinute: 6000}}, httpx.NoJitter),
		httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute}), httpx.ClientConfig{Provider: "metron", UserAgent: "test", Transport: answer{}})
	return metron.New(client, func() (string, string) { return user, pass })
}

func TestSeriesAndIssuesCarryTheBridgeIDs(t *testing.T) {
	c := newClient("owner", "secret")
	series, err := c.SearchSeries(t.Context(), "night harbor", 2019)
	want := []metron.Series{{ID: 55, Name: "Night Harbor", Volume: 1, YearBegan: 2019, YearEnded: 2021, Type: "Limited Series", Publisher: "Acme Press", IssueCount: 6, Status: "Completed", ComicVineID: 1001, GCDID: 500}}
	if err != nil || !reflect.DeepEqual(series, want) {
		t.Fatalf("series = %+v, %v", series, err)
	}
	issues, more, err := c.Issues(t.Context(), 55, 1)
	if err != nil || !more || len(issues) != 1 || issues[0].StoreDate != "2019-04-10" || issues[0].SeriesID != 55 {
		t.Fatalf("issues = %+v more=%v err=%v", issues, more, err)
	}
	issue, err := c.Issue(t.Context(), 900)
	wantIssue := metron.Issue{ID: 900, SeriesID: 55, Number: "1", Titles: []string{"Dawn"}, CoverDate: "2019-06-01", StoreDate: "2019-04-10", Pages: 32, Price: "3.99", Rating: "Teen",
		Summary: "d", CoverURL: "https://img.example/1.jpg", Credits: []metron.Credit{{Name: "Ann Quill", Roles: []string{"Writer", "Cover"}}}, Arcs: []string{"Long Night"},
		Reprints: []int{901, 902}, ComicVineID: 5001, GCDID: 7001}
	if err != nil || !reflect.DeepEqual(issue, wantIssue) {
		t.Fatalf("issue = %+v, %v", issue, err)
	}
}

func TestNoAccountNotFoundAndRejectedAccount(t *testing.T) {
	if _, err := newClient("", "").Series(t.Context(), 55); !errors.Is(err, metron.ErrNoAccount) {
		t.Errorf("err = %v; want ErrNoAccount", err)
	}
	if _, err := newClient("owner", "secret").Series(t.Context(), 1); !errors.Is(err, metron.ErrNotFound) {
		t.Errorf("err = %v; want ErrNotFound", err)
	}
	if _, err := newClient("owner", "wrong").Series(t.Context(), 55); err == nil || errors.Is(err, metron.ErrNotFound) {
		t.Errorf("err = %v; want a rejected-account error", err)
	}
}
