package adapters

import (
	"context"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/importing/arrimport"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// radarrAnswers is a Radarr with three movies, one of them without a TMDB id.
type radarrAnswers struct{}

func (radarrAnswers) RoundTrip(request *http.Request) (*http.Response, error) {
	body := map[string]string{
		"/api/v3/qualityprofile": `[]`,
		"/api/v3/movie":          `[{"id":1,"title":"A","tmdbId":603},{"id":2,"title":"B","tmdbId":0,"imdbId":"tt1"},{"id":3,"title":"C","tmdbId":78}]`,
	}[request.URL.Path]
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
}

// A Radarr import reads Wikidata for all its films in bulk before the walk: one serial query per film made a
// 3,964-film import walk two films a minute (found live 2026-10-21).
func TestRadarrImportWarmsWikidataWithEveryTMDBIDBeforeTheWalk(t *testing.T) {
	fake := clock.NewFakeAtEpoch()
	client := httpx.NewClient(fake, httpx.NewGuard(nil, nil, nil),
		httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{RequestsPerMinute: 6000}}, httpx.NoJitter),
		httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute}),
		httpx.ClientConfig{Provider: "radarr-test", UserAgent: "test", Transport: radarrAnswers{}})
	var warmed []string
	sync := ArrSync{Clock: fake, WarmFilms: func(_ context.Context, ids []string) error {
		warmed = append(warmed, ids...)
		return nil
	}}
	movies, err := sync.radarrSource(1, arrimport.NewRadarrClient(client, "http://radarr.local", "key")).list(t.Context())
	if err != nil || len(movies) != 3 {
		t.Fatalf("list = %d movies, %v", len(movies), err)
	}
	if !slices.Equal(warmed, []string{"603", "78"}) {
		t.Errorf("warmed %v, want every TMDB id", warmed)
	}
}
