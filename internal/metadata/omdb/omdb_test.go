package omdb_test

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/metadata/omdb"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

type answer struct{}

func (answer) RoundTrip(request *http.Request) (*http.Response, error) {
	body := `{"Response":"False","Error":"Incorrect IMDb ID."}`
	if request.URL.Query().Get("i") == "tt9000001" && request.URL.Query().Get("apikey") == "owner-key" {
		body = `{"Title":"Example Film","Year":"1999","Rated":"PG-13","Plot":"A long plot.","imdbID":"tt9000001","imdbVotes":"1,234",
		  "Ratings":[{"Source":"Internet Movie Database","Value":"8.1/10"},{"Source":"Rotten Tomatoes","Value":"97%"}],"Response":"True"}`
	}
	if request.URL.Query().Get("i") == "tt9000002" {
		body = `{"Response":"False","Error":"Movie not found!"}`
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
}

func TestAFilmIsReadByIMDbIDAndNotFoundIsToldApartFromAFailure(t *testing.T) {
	fake := clock.NewFakeAtEpoch()
	http := httpx.NewClient(fake, httpx.NewGuard(nil, nil, nil), httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{RequestsPerMinute: 6000}}, httpx.NoJitter),
		httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute}), httpx.ClientConfig{Provider: "omdb", UserAgent: "test", Transport: answer{}})
	films := omdb.New(http, func() string { return "owner-key" })
	movie, err := films.Movie(t.Context(), "tt9000001")
	if err != nil || movie.Plot != "A long plot." || movie.IMDbVotes != "1,234" || len(movie.Ratings) != 2 || movie.Ratings[1].Value != "97%" {
		t.Fatalf("movie = %+v, %v", movie, err)
	}
	if _, err := films.Movie(t.Context(), "tt9000002"); !errors.Is(err, omdb.ErrNotFound) {
		t.Errorf("err = %v; want ErrNotFound", err)
	}
	if _, err := films.Movie(t.Context(), "tt1"); err == nil || errors.Is(err, omdb.ErrNotFound) {
		t.Errorf("err = %v; want OMDb's own error, not 'not found'", err)
	}
}
