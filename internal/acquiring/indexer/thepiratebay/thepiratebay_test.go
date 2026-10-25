package thepiratebay_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/clienttest"
	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/indexer/thepiratebay"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

func TestSearch_MapsApibayAndDropsItsNoResultsRow(t *testing.T) {
	server := &clienttest.Server{Routes: map[string]clienttest.Route{"/q.php": {Body: `[
	 {"id":"77","name":"Heka - Zanuvode (1988) [FLAC]","info_hash":"0123456789ABCDEF0123456789ABCDEF01234567","seeders":"12","leechers":"3","size":"300000000","added":"1700000000","category":"104"},
	 {"id":"78","name":"Some Show S01E01","info_hash":"1123456789abcdef0123456789abcdef01234567","seeders":"5","leechers":"0","size":"1","added":"1700000000","category":"208"},
	 {"id":"0","name":"No results returned","info_hash":"0000000000000000000000000000000000000000","seeders":"0","leechers":"0","size":"0","added":"0","category":"0"}]`}}}
	got, err := thepiratebay.New(httpClient(t, server), "https://apibay.org/").Search(t.Context(),
		indexer.SearchRequest{Function: "music", Query: "Heka Zanuvode", Categories: []int{3000}})
	if err != nil || len(got) != 1 {
		t.Fatalf("Search = %+v, %v; want the album alone", got, err)
	}
	if server.Seen[0].Query != "q=Heka+Zanuvode" {
		t.Errorf("query = %q", server.Seen[0].Query)
	}
	want := indexer.RawItem{
		Title: "Heka - Zanuvode (1988) [FLAC]", GUID: "https://thepiratebay.org/description.php?id=77", Categories: []int{3040},
		SizeBytes: 300000000, PublishedAt: time.Unix(1700000000, 0).UTC(), Seeders: 12, Peers: 15,
		InfoHash: "0123456789ABCDEF0123456789ABCDEF01234567",
	}
	link := got[0].Link
	got[0].Link = ""
	if !reflect.DeepEqual(got[0], want) || !strings.Contains(link, "xt=urn%3Abtih%3A0123456789abcdef0123456789abcdef01234567") {
		t.Errorf("item = %+v link %s", got[0], link)
	}
}

func TestSearch_NoWordsReadsTheNewest(t *testing.T) {
	server := &clienttest.Server{Routes: map[string]clienttest.Route{"/precompiled/data_top100_recent.json": {Body: `[
	 {"id":1,"name":"New","info_hash":"2123456789abcdef0123456789abcdef01234567","seeders":1,"leechers":1,"size":5,"added":1700000000,"category":101}]`}}}
	got, err := thepiratebay.New(httpClient(t, server), "https://apibay.org").Search(t.Context(), indexer.SearchRequest{})
	if err != nil || len(got) != 1 || got[0].Categories[0] != 3000 {
		t.Errorf("Search = %+v, %v", got, err)
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
