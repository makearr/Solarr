package knaben_test

import (
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/clienttest"
	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/indexer/knaben"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

func TestSearch_PostsTheQueryAndMapsCategories(t *testing.T) {
	server := &clienttest.Server{Routes: map[string]clienttest.Route{"POST /v1": {Body: `{"hits":[
	 {"title":"Heka - Zanuvode FLAC","hash":"ABC","magnetUrl":"magnet:?xt=urn:btih:abc","details":"https://x/1","seeders":9,"peers":1,"bytes":100,"date":"2024-01-01T00:00:00Z","categoryId":[1002000]},
	 {"title":"Film","hash":"DEF","link":"https://x/2.torrent","seeders":1,"peers":0,"bytes":1,"date":"2024-01-01T00:00:00Z","categoryId":[3001000]}]}`}}}
	got, err := knaben.New(httpClient(t, server), "https://api.knaben.org").Search(t.Context(), indexer.SearchRequest{Query: "Heka", Categories: []int{3000}})
	if err != nil || len(got) != 1 {
		t.Fatalf("Search = %+v, %v", got, err)
	}
	if got[0].Categories[0] != 3040 || got[0].Link != "magnet:?xt=urn:btih:abc" || got[0].GUID != "https://x/1" || got[0].InfoHash != "abc" {
		t.Errorf("item = %+v", got[0])
	}
	if body := server.Seen[0].Body; !strings.Contains(body, `"query":"Heka"`) || !strings.Contains(body, `"hide_xxx":true`) {
		t.Errorf("body = %s", body)
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
