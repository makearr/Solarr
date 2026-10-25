package passthepopcorn_test

import (
	"net/url"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/clienttest"
	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/indexer/passthepopcorn"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

func TestSearch_SendsTheAPIHeadersAndBuildsTheDownloadLink(t *testing.T) {
	server := &clienttest.Server{Routes: map[string]clienttest.Route{"/torrents.php": {Body: `{"AuthKey":"ak","PassKey":"pk","Movies":[
	 {"Title":"The Matrix","Year":"1999","Torrents":[{"Id":"3","ReleaseName":"The.Matrix.1999.2160p.UHD","Size":"900","Seeders":"10","Leechers":"1","UploadTime":"2020-01-02 03:04:05","Resolution":"2160p"}]}]}`}}}
	got, err := passthepopcorn.New(httpClient(t, server), "https://passthepopcorn.me", "user:key").Search(t.Context(),
		indexer.SearchRequest{Extra: map[string]string{"imdbid": "0133093"}})
	if err != nil || len(got) != 1 {
		t.Fatalf("Search = %+v, %v", got, err)
	}
	seen := server.Seen[0]
	query, _ := url.ParseQuery(seen.Query)
	if seen.Header.Get("ApiUser") != "user" || seen.Header.Get("ApiKey") != "key" || query.Get("searchstr") != "tt0133093" {
		t.Errorf("request = %+v", seen)
	}
	link, _ := url.Parse(got[0].Link)
	if link.Query().Get("authkey") != "ak" || link.Query().Get("torrent_pass") != "pk" || got[0].Categories[0] != 2045 || got[0].SizeBytes != 900 {
		t.Errorf("item = %+v", got[0])
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
