package yts_test

import (
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/clienttest"
	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/indexer/yts"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

func TestSearch_NamesEachTorrentSoItsQualityParses(t *testing.T) {
	server := &clienttest.Server{Routes: map[string]clienttest.Route{"/api/v2/list_movies.json": {Body: `{"status":"ok","data":{"movies":[
	 {"title":"The Matrix","year":1999,"torrents":[
	  {"url":"https://yts.mx/torrent/download/AAA","hash":"AAA","quality":"1080p","type":"bluray","video_codec":"x264","seeds":100,"peers":5,"size_bytes":2000,"date_uploaded_unix":1700000000},
	  {"url":"https://yts.mx/torrent/download/BBB","hash":"BBB","quality":"2160p","type":"web","seeds":10,"peers":1,"size_bytes":6000,"date_uploaded_unix":1700000000}]}]}}`}}}
	got, err := yts.New(httpClient(t, server), "https://yts.mx").Search(t.Context(),
		indexer.SearchRequest{Function: "movie", Extra: map[string]string{"imdbid": "0133093"}, Categories: []int{2000}})
	if err != nil || len(got) != 2 {
		t.Fatalf("Search = %+v, %v", got, err)
	}
	if server.Seen[0].Query != "limit=50&query_term=tt0133093" {
		t.Errorf("query = %q", server.Seen[0].Query)
	}
	if got[0].Title != "The Matrix (1999) [1080p] [BluRay] [x264] [YTS.MX]" || got[1].Title != "The Matrix (1999) [2160p] [WEBRip] [YTS.MX]" ||
		got[1].Categories[0] != 2045 || got[0].Peers != 105 {
		t.Errorf("items = %+v", got)
	}
	if none, err := yts.New(httpClient(t, server), "https://yts.mx").Search(t.Context(), indexer.SearchRequest{Query: "x", Categories: []int{3000}}); err != nil || none != nil {
		t.Errorf("a music search = %+v, %v; want none and no request", none, err)
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
