package broadcasthenet_test

import (
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/clienttest"
	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/indexer/broadcasthenet"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

func TestSearch_AsksByTVDBSeasonAndEpisode(t *testing.T) {
	server := &clienttest.Server{Routes: map[string]clienttest.Route{"POST /": {Body: `{"id":1,"result":{"results":"1","torrents":{
	 "100":{"TorrentID":"100","ReleaseName":"Show.S01E02.720p.HDTV","Size":"500","Seeders":"20","Leechers":"1","Time":"1700000000","Resolution":"720p","InfoHash":"ABC","DownloadURL":"https://broadcasthe.net/torrents.php?action=download&id=100"}}}}`}}}
	got, err := broadcasthenet.New(httpClient(t, server), "https://api.broadcasthe.net", "key").Search(t.Context(),
		indexer.SearchRequest{Function: "tvsearch", Extra: map[string]string{"tvdbid": "123", "season": "1", "ep": "2"}, Categories: []int{5000}})
	if err != nil || len(got) != 1 || got[0].Title != "Show.S01E02.720p.HDTV" || got[0].Categories[0] != 5040 || got[0].SizeBytes != 500 {
		t.Fatalf("Search = %+v, %v", got, err)
	}
	body := server.Seen[0].Body
	if !strings.Contains(body, `"getTorrents"`) || !strings.Contains(body, `"name":"S01E02"`) || !strings.Contains(body, `"tvdb":"123"`) || !strings.Contains(body, `"key"`) {
		t.Errorf("body = %s", body)
	}
	server.Routes["POST /"] = clienttest.Route{Body: `{"id":1,"error":{"code":-32001,"message":"Invalid API Key"}}`}
	if _, err := broadcasthenet.New(httpClient(t, server), "https://api.broadcasthe.net", "bad").Capabilities(t.Context()); err == nil {
		t.Error("a refused key passed the test")
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
