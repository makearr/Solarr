package comicvine_test

import (
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/metadata/comicvine"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

type answer struct{ paths *[]string }

func (a answer) RoundTrip(request *http.Request) (*http.Response, error) {
	*a.paths = append(*a.paths, request.URL.Path+"?"+request.URL.RawQuery)
	query := request.URL.Query()
	body := `{"error":"Invalid API Key","status_code":100,"results":[]}`
	if query.Get("api_key") == "owner-key" && query.Get("format") == "json" {
		switch request.URL.Path {
		case "/api/search/":
			body = `{"error":"OK","status_code":1,"results":[
			 {"id":1001,"name":"Night Harbor","start_year":"2019","publisher":{"id":9,"name":"Acme Press"},"count_of_issues":12,"image":{"original_url":"https://img.example/1.jpg"}},
			 {"id":1002,"name":"Night Harbor","start_year":"1987","publisher":null,"count_of_issues":3}]}`
		case "/api/volume/4050-1001/":
			body = `{"error":"OK","status_code":1,"results":{"id":1001,"name":"Night Harbor","start_year":"2019","publisher":{"name":"Acme Press"},"count_of_issues":12}}`
		case "/api/volume/4050-9999/":
			body = `{"error":"Object Not Found","status_code":101,"results":[]}`
		case "/api/issues/":
			body = `{"error":"OK","status_code":1,"results":[{"id":5001,"issue_number":"1","name":"Dawn","cover_date":"2019-06-01","store_date":"2019-04-10","volume":{"id":1001,"name":"Night Harbor"}}]}`
		case "/api/issue/4000-5001/":
			body = `{"error":"OK","status_code":1,"results":{"id":5001,"issue_number":"1","name":"Dawn","volume":{"id":1001},
			 "person_credits":[{"id":70,"name":"Ann Quill","role":"writer, penciler"},{"id":70,"name":"Ann Quill","role":"cover"}],
			 "story_arc_credits":[{"id":300,"name":"Long Night"}]}}`
		}
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
}

func newClient(key string, paths *[]string) comicvine.Client {
	fake := clock.NewFakeAtEpoch()
	client := httpx.NewClient(fake, httpx.NewGuard(nil, nil, nil), httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{RequestsPerMinute: 6000}}, httpx.NoJitter),
		httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute}), httpx.ClientConfig{Provider: "comicvine", UserAgent: "test", Transport: answer{paths}})
	return comicvine.New(client, func() string { return key })
}

func TestSearchKeepsSeriesNearTheYear(t *testing.T) {
	var paths []string
	got, err := newClient("owner-key", &paths).SearchSeries(t.Context(), "night harbor", 2019)
	if err != nil {
		t.Fatal(err)
	}
	want := []comicvine.Series{{ID: 1001, Name: "Night Harbor", StartYear: 2019, Publisher: "Acme Press", IssueCount: 12, CoverURL: "https://img.example/1.jpg"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
	all, _ := newClient("owner-key", &paths).SearchSeries(t.Context(), "night harbor", 0)
	if len(all) != 2 {
		t.Fatalf("no year: got %d series, want 2", len(all))
	}
}

func TestSeriesIssuesAndIssue(t *testing.T) {
	var paths []string
	c := newClient("owner-key", &paths)
	series, err := c.Series(t.Context(), 1001)
	if err != nil || series.Name != "Night Harbor" || series.IssueCount != 12 || series.Publisher != "Acme Press" {
		t.Fatalf("series = %+v, %v", series, err)
	}
	issues, more, err := c.Issues(t.Context(), 1001, 0)
	wantIssues := []comicvine.Issue{{ID: 5001, SeriesID: 1001, Number: "1", Title: "Dawn", CoverDate: "2019-06-01", StoreDate: "2019-04-10"}}
	if err != nil || more || !reflect.DeepEqual(issues, wantIssues) {
		t.Fatalf("issues = %+v more=%v err=%v", issues, more, err)
	}
	issue, err := c.Issue(t.Context(), 5001)
	wantIssue := comicvine.Issue{ID: 5001, SeriesID: 1001, Number: "1", Title: "Dawn",
		Credits: []comicvine.Credit{{ID: 70, Name: "Ann Quill", Roles: []string{"writer", "penciler", "cover"}}}, Arcs: []comicvine.Arc{{ID: 300, Name: "Long Night"}}}
	if err != nil || !reflect.DeepEqual(issue, wantIssue) {
		t.Fatalf("issue = %+v, %v", issue, err)
	}
	if !strings.Contains(paths[len(paths)-2], "filter=volume%3A1001") && !strings.Contains(strings.Join(paths, " "), "filter=volume%3A1001") {
		t.Errorf("issues were not filtered by volume: %v", paths)
	}
}

func TestNotFoundNoKeyAndRejectedKey(t *testing.T) {
	var paths []string
	if _, err := newClient("owner-key", &paths).Series(t.Context(), 9999); !errors.Is(err, comicvine.ErrNotFound) {
		t.Errorf("err = %v; want ErrNotFound", err)
	}
	if _, err := newClient("", &paths).Series(t.Context(), 1001); !errors.Is(err, comicvine.ErrNoKey) {
		t.Errorf("err = %v; want ErrNoKey", err)
	}
	if _, err := newClient("wrong", &paths).Series(t.Context(), 1001); err == nil || errors.Is(err, comicvine.ErrNotFound) {
		t.Errorf("err = %v; want ComicVine's own error", err)
	}
}
