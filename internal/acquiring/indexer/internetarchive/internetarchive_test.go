package internetarchive_test

import (
	"net/url"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/clienttest"
	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/indexer/internetarchive"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

func TestSearch_AsksForTorrentItemsAndLinksTheirTorrent(t *testing.T) {
	server := &clienttest.Server{Routes: map[string]clienttest.Route{"/advancedsearch.php": {Body: `{"response":{"docs":[
	 {"identifier":"gd1977-05-08","title":"Grateful Dead Live at Barton Hall","item_size":123456,"publicdate":"2004-01-01T00:00:00Z","mediatype":"etree"}]}}`}}}
	got, err := internetarchive.New(httpClient(t, server), "https://archive.org").Search(t.Context(), indexer.SearchRequest{Query: "Grateful Dead"})
	if err != nil || len(got) != 1 {
		t.Fatalf("Search = %+v, %v", got, err)
	}
	if got[0].Link != "https://archive.org/download/gd1977-05-08/gd1977-05-08_archive.torrent" || got[0].Categories[0] != 3000 || got[0].SizeBytes != 123456 {
		t.Errorf("item = %+v", got[0])
	}
	query, _ := url.ParseQuery(server.Seen[0].Query)
	if query.Get("q") != `(Grateful Dead) AND format:("Archive BitTorrent")` {
		t.Errorf("q = %q", query.Get("q"))
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
