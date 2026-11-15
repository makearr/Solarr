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

// IDX-6: a special is asked for by number, season 0 alone as episodes only, a daily episode by its date.
func TestSearch_SpecialsAndDailyEpisodes(t *testing.T) {
	for name, tc := range map[string]struct {
		extra map[string]string
		want  []string
		not   string
	}{
		"special":      {map[string]string{"season": "0", "ep": "5"}, []string{`"name":"S00E05"`, `"category":"Episode"`}, ""},
		"all specials": {map[string]string{"season": "0"}, []string{`"category":"Episode"`}, `"name"`},
		"daily":        {map[string]string{"season": "2024", "ep": "05/01"}, []string{`"name":"2024.05.01"`, `"category":"Episode"`}, ""},
		"season pack":  {map[string]string{"season": "2"}, []string{`"name":"Season 2"`, `"category":"Season"`}, ""},
	} {
		server := &clienttest.Server{Routes: map[string]clienttest.Route{"POST /": {Body: `{"id":1,"result":{"results":"0","torrents":{}}}`}}}
		tc.extra["tvdbid"] = "123"
		if _, err := broadcasthenet.New(httpClient(t, server), "https://api.broadcasthe.net", "key").Search(t.Context(),
			indexer.SearchRequest{Function: "tvsearch", Extra: tc.extra, Categories: []int{5000}}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		body := server.Seen[0].Body
		for _, want := range tc.want {
			if !strings.Contains(body, want) {
				t.Errorf("%s: body = %s, want %s", name, body, want)
			}
		}
		if tc.not != "" && strings.Contains(body, tc.not) {
			t.Errorf("%s: body = %s, must not hold %s", name, body, tc.not)
		}
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
