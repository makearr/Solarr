package hdbits_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/clienttest"
	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/indexer/hdbits"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

func TestSearch_AsksByIMDbIDAndLinksWithThePasskey(t *testing.T) {
	server := &clienttest.Server{Routes: map[string]clienttest.Route{"POST /api/torrents": {Body: `{"status":0,"data":[
	 {"id":9,"hash":"ABC","name":"The.Matrix.1999.1080p.BluRay.x264-GRP","size":100,"seeders":50,"leechers":2,"added":"2020-01-02T03:04:05+0000","type_category":1}]}`}}}
	got, err := hdbits.New(httpClient(t, server), "https://hdbits.org", "me:pk").Search(t.Context(),
		indexer.SearchRequest{Function: "movie", Extra: map[string]string{"imdbid": "0133093"}, Categories: []int{2000}})
	if err != nil || len(got) != 1 {
		t.Fatalf("Search = %+v, %v", got, err)
	}
	if body := server.Seen[0].Body; !strings.Contains(body, `"imdb":{"id":133093}`) || !strings.Contains(body, `"passkey":"pk"`) {
		t.Errorf("body = %s", body)
	}
	if got[0].Link != "https://hdbits.org/download.php?id=9&passkey=pk" || got[0].InfoHash != "abc" || got[0].Categories[0] != 2000 {
		t.Errorf("item = %+v", got[0])
	}
	server.Routes["POST /api/torrents"] = clienttest.Route{Body: `{"status":5,"message":"Auth failed"}`}
	if _, err := hdbits.New(httpClient(t, server), "https://hdbits.org", "me:bad").Capabilities(t.Context()); err == nil {
		t.Error("a refused passkey passed the test")
	}
}

// IDX-6: season 0 is sent as the specials, not dropped as "any season".
func TestSearch_SendsSeasonZeroForSpecials(t *testing.T) {
	for name, tc := range map[string]struct {
		extra map[string]string
		want  string
	}{
		"special": {map[string]string{"season": "0", "ep": "5"}, `"tvdb":{"id":123,"season":0,"episode":5}`},
		"season":  {map[string]string{"season": "2"}, `"tvdb":{"id":123,"season":2}`},
		"series":  {map[string]string{}, `"tvdb":{"id":123}`},
		"daily":   {map[string]string{"season": "2024", "ep": "05/01"}, `"tvdb":{"id":123}`},
	} {
		server := &clienttest.Server{Routes: map[string]clienttest.Route{"POST /api/torrents": {Body: `{"status":0,"data":[]}`}}}
		tc.extra["tvdbid"] = "123"
		if _, err := hdbits.New(httpClient(t, server), "https://hdbits.org", "me:pk").Search(t.Context(),
			indexer.SearchRequest{Function: "tvsearch", Extra: tc.extra, Categories: []int{5000}}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if body := server.Seen[0].Body; !strings.Contains(body, tc.want) {
			t.Errorf("%s: body = %s, want %s", name, body, tc.want)
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

// IDX-1: the ids the tracker lists with a release are carried; an absent or zero one is not.
func TestSearch_CarriesTheIDsTheTrackerListed(t *testing.T) {
	server := &clienttest.Server{Routes: map[string]clienttest.Route{"POST /api/torrents": {Body: `{"status":0,"data":[
	 {"id":9,"hash":"ABC","name":"Show.S01E02.1080p","size":1,"seeders":1,"leechers":0,"added":"2020-01-02T03:04:05+0000","type_category":2,"imdb":{"id":133093},"tvdb":{"id":81189}},
	 {"id":10,"hash":"DEF","name":"Other.1080p","size":1,"seeders":1,"leechers":0,"added":"2020-01-02T03:04:05+0000","type_category":2,"imdb":{"id":0},"tvdb":null}]}`}}}
	got, err := hdbits.New(httpClient(t, server), "https://hdbits.org", "me:pk").Search(t.Context(), indexer.SearchRequest{Function: "tvsearch"})
	if err != nil || len(got) != 2 {
		t.Fatalf("Search = %+v, %v", got, err)
	}
	if fmt.Sprint(got[0].IDs) != "map[imdb:tt133093 tvdb:81189]" || got[1].IDs != nil {
		t.Errorf("ids = %v and %v", got[0].IDs, got[1].IDs)
	}
}
