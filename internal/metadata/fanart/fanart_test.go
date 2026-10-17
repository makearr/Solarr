package fanart_test

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/metadata/fanart"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

type answer struct{}

func (answer) RoundTrip(request *http.Request) (*http.Response, error) {
	status, body := http.StatusNotFound, `{"status":"error","error message":"Not found"}`
	if request.URL.Path == "/v3/movies/7001" && request.URL.Query().Get("api_key") == "owner-key" {
		status, body = http.StatusOK, `{"name":"Example Film","hdmovielogo":[{"id":"1","url":"https://assets.fanart.tv/logo.png","lang":"en","likes":"4"}],
		  "moviedisc":[{"id":"2","url":"https://assets.fanart.tv/disc.png","lang":"en","likes":"1","disc":"1","disc_type":"bluray"}]}`
	}
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
}

func TestAFilmsPicturesAreReadByTMDBID(t *testing.T) {
	fake := clock.NewFakeAtEpoch()
	http := httpx.NewClient(fake, httpx.NewGuard(nil, nil, nil), httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{RequestsPerMinute: 6000}}, httpx.NoJitter),
		httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute}), httpx.ClientConfig{Provider: "fanart", UserAgent: "test", Transport: answer{}})
	pictures := fanart.New(http, func() string { return "owner-key" })
	movie, err := pictures.Movie(t.Context(), 7001, "")
	if err != nil || len(movie.Logos) != 1 || movie.Logos[0].URL != "https://assets.fanart.tv/logo.png" || len(movie.Discs) != 1 {
		t.Fatalf("movie = %+v, %v", movie, err)
	}
	if _, err := pictures.Movie(t.Context(), 1, ""); !errors.Is(err, fanart.ErrNotFound) {
		t.Errorf("err = %v; want ErrNotFound", err)
	}
}
