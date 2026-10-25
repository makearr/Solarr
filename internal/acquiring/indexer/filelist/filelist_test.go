package filelist_test

import (
	"net/url"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/clienttest"
	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/indexer/filelist"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

func TestSearch_SignsInAndMapsCategoryNames(t *testing.T) {
	server := &clienttest.Server{Routes: map[string]clienttest.Route{"/api.php": {Body: `[
	 {"id":5,"name":"Heka-Zanuvode-FLAC","category":"FLAC","size":10,"seeders":4,"leechers":1,"upload_date":"2020-01-02 03:04:05","download_link":"https://filelist.io/download.php?id=5&passkey=p"},
	 {"id":6,"name":"Show.S01E01","category":"Seriale HD","size":10,"seeders":1,"leechers":0,"upload_date":"2020-01-02 03:04:05","download_link":"https://x"}]`}}}
	got, err := filelist.New(httpClient(t, server), "https://filelist.io", "me:p").Search(t.Context(), indexer.SearchRequest{Query: "Heka", Categories: []int{3000}})
	if err != nil || len(got) != 1 || got[0].Categories[0] != 3040 || got[0].GUID != "https://filelist.io/details.php?id=5" {
		t.Fatalf("Search = %+v, %v", got, err)
	}
	query, _ := url.ParseQuery(server.Seen[0].Query)
	if query.Get("username") != "me" || query.Get("passkey") != "p" || query.Get("action") != "search-torrents" || query.Get("query") != "Heka" {
		t.Errorf("query = %v", query)
	}
	server.Routes["/api.php"] = clienttest.Route{Body: `{"error":"Invalid username/passkey"}`}
	if _, err := filelist.New(httpClient(t, server), "https://filelist.io", "me:wrong").Capabilities(t.Context()); err == nil {
		t.Error("a refused passkey passed the test")
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
