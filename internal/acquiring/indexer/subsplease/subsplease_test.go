package subsplease_test

import (
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/clienttest"
	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/indexer/subsplease"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

func TestSearch_IsOneReleasePerResolutionAndNothingIsAnEmptyArray(t *testing.T) {
	server := &clienttest.Server{Routes: map[string]clienttest.Route{"/api/": {Body: `{"Show - 01":{"show":"Show","episode":"01","release_date":"10/15/22","page":"show",
	 "downloads":[{"res":"720","magnet":"magnet:?xt=urn:btih:a&xl=700"},{"res":"1080","magnet":"magnet:?xt=urn:btih:b&xl=1400"}]}}`}}}
	got, err := subsplease.New(httpClient(t, server), "https://subsplease.org").Search(t.Context(), indexer.SearchRequest{Query: "Show"})
	if err != nil || len(got) != 2 || got[1].Title != "[SubsPlease] Show - 01 (1080p)" || got[1].SizeBytes != 1400 || got[0].Categories[0] != 5070 {
		t.Fatalf("Search = %+v, %v", got, err)
	}
	server.Routes["/api/"] = clienttest.Route{Body: "[]"}
	if none, err := subsplease.New(httpClient(t, server), "https://subsplease.org").Search(t.Context(), indexer.SearchRequest{Query: "Nothing"}); err != nil || len(none) != 0 {
		t.Errorf("nothing found = %+v, %v", none, err)
	}
}

// httpClient is an httpx.Client over server, with limits no test reaches.
func httpClient(t *testing.T, server *clienttest.Server) *httpx.Client {
	t.Helper()
	fake := clock.NewFakeAtEpoch()
	limiter := httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{RequestsPerMinute: 6000}}, httpx.NoJitter)
	breaker := httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute})
	return httpx.NewClient(fake, httpx.NewGuard(nil, nil, nil), limiter, breaker, httpx.ClientConfig{
		Provider: "indexer-test", UserAgent: "test", Transport: server,
	})
}
