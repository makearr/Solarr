package internetarchive_test

import (
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/metadata/internetarchive"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

type answer struct{}

func (answer) RoundTrip(request *http.Request) (*http.Response, error) {
	body := `{"response":{"docs":[]}}`
	switch {
	case request.URL.Path == "/advancedsearch.php" && strings.Contains(request.URL.Query().Get("q"), "9780000000001"):
		body = `{"response":{"docs":[{"identifier":"example-fox"}]}}`
	case request.URL.Path == "/metadata/example-fox":
		body = `{"metadata":{"title":"Example Fox","description":["90 pages : 20 cm","A fox digs under three farms to feed his family, and the farmers dig after him with everything they own."],"subject":["Foxes -- Fiction","Farmers -- Fiction"],"language":"eng","publisher":"Reef Books","date":"1988"}}`
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
}

func TestAnISBNFindsTheTextAndItsMetadata(t *testing.T) {
	fake := clock.NewFakeAtEpoch()
	client := httpx.NewClient(fake, httpx.NewGuard(nil, nil, nil), httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{RequestsPerMinute: 6000}}, httpx.NoJitter),
		httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute}), httpx.ClientConfig{Provider: "internetarchive", UserAgent: "test", Transport: answer{}})
	got, err := internetarchive.New(client).ByISBN(t.Context(), "9780000000001")
	want := internetarchive.Item{
		Identifier: "example-fox", Title: "Example Fox", Description: "A fox digs under three farms to feed his family, and the farmers dig after him with everything they own.",
		Subjects: []string{"Foxes -- Fiction", "Farmers -- Fiction"}, Language: "eng", Publisher: "Reef Books", Date: "1988", Cover: "https://archive.org/services/img/example-fox",
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("item = %+v, %v\nwant %+v", got, err, want)
	}
	if _, err := internetarchive.New(client).ByISBN(t.Context(), "9780000000002"); !errors.Is(err, internetarchive.ErrNotFound) {
		t.Errorf("err = %v; want ErrNotFound", err)
	}
}
