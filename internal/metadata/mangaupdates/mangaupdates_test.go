package mangaupdates_test

import (
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/metadata/mangaupdates"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

const record = `{"series_id":4242,"title":"Silent Tide","associated":[{"title":"The Silent Tide"}],"description":"d","image":{"url":{"original":"https://img.example/s.jpg"}},
"type":"Manga","year":"2014","status":"12 Volumes (Complete)","licensed":true,"completed":true,"genres":[{"genre":"Drama"}],
"authors":[{"name":"Mio Sato","type":"Author"}],"publishers":[{"publisher_name":"Acme Books","type":"English"}]}`

type answer struct{}

func (answer) RoundTrip(request *http.Request) (*http.Response, error) {
	status := http.StatusOK
	var body string
	switch {
	case request.URL.Path == "/v1/series/4242":
		body = record
	case request.URL.Path == "/v1/series/search" && request.Method == http.MethodPost:
		body = `{"total_hits":40,"results":[{"record":` + record + `}]}`
	default:
		status, body = http.StatusNotFound, `{"reason":"Series not found"}`
	}
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
}

func newClient() mangaupdates.Client {
	fake := clock.NewFakeAtEpoch()
	return mangaupdates.New(httpx.NewClient(fake, httpx.NewGuard(nil, nil, nil), httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{RequestsPerMinute: 6000}}, httpx.NoJitter),
		httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute}), httpx.ClientConfig{Provider: "mangaupdates", UserAgent: "test", Transport: answer{}}))
}

func TestSeriesAndSearch(t *testing.T) {
	want := mangaupdates.Series{ID: 4242, Title: "Silent Tide", Associated: []string{"The Silent Tide"}, Description: "d", CoverURL: "https://img.example/s.jpg", Type: "Manga", Year: 2014,
		Status: "12 Volumes (Complete)", Licensed: true, Completed: true, Genres: []string{"Drama"}, Authors: []mangaupdates.Author{{Name: "Mio Sato", Type: "Author"}},
		Publishers: []mangaupdates.Publisher{{Name: "Acme Books", Type: "English"}}, Volumes: 12}
	got, err := newClient().Series(t.Context(), 4242)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("series = %+v, %v", got, err)
	}
	found, more, err := newClient().SearchSeries(t.Context(), "silent tide", 1)
	if err != nil || !more || len(found) != 1 || !reflect.DeepEqual(found[0], want) {
		t.Fatalf("found = %+v more=%v err=%v", found, more, err)
	}
	if _, err := newClient().Series(t.Context(), 1); !errors.Is(err, mangaupdates.ErrNotFound) {
		t.Errorf("err = %v; want ErrNotFound", err)
	}
}

func TestIDFromCode(t *testing.T) {
	if id, ok := mangaupdates.IDFromCode("8JQX4PM"); !ok || id != 36*36*36*36*36*36*8+19*36*36*36*36*36+26*36*36*36*36+33*36*36*36+4*36*36+25*36+22 {
		t.Errorf("id = %d ok=%v", id, ok)
	}
	if _, ok := mangaupdates.IDFromCode("not a code!"); ok {
		t.Error("a non-code was accepted")
	}
}
