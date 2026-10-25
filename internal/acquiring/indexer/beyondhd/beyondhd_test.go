package beyondhd_test

import (
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/clienttest"
	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/indexer/beyondhd"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

func TestSearch_PutsTheAPIKeyInThePathAndTheRSSKeyInTheBody(t *testing.T) {
	server := &clienttest.Server{Routes: map[string]clienttest.Route{"POST /api/torrents/apikey": {Body: `{"status_code":1,"success":true,"results":[
	 {"name":"Show.S01.1080p","url":"https://beyond-hd.me/details/1","download_url":"https://beyond-hd.me/dl/1","category":"TV","size":10,"seeders":3,"leechers":0,"created_at":"2020-01-02 03:04:05"}]}`}}}
	got, err := beyondhd.New(httpClient(t, server), "https://beyond-hd.me", "rss:apikey").Search(t.Context(), indexer.SearchRequest{Query: "Show", Categories: []int{5000}})
	if err != nil || len(got) != 1 || got[0].Categories[0] != 5000 || got[0].Link != "https://beyond-hd.me/dl/1" {
		t.Fatalf("Search = %+v, %v", got, err)
	}
	if body := server.Seen[0].Body; !strings.Contains(body, `"rsskey":"rss"`) || !strings.Contains(body, `"search":"Show"`) {
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
