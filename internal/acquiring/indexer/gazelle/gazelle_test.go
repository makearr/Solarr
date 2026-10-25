package gazelle_test

import (
	"net/url"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/clienttest"
	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/indexer/gazelle"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

const browse = `{"status":"success","response":{"results":[{"groupId":1,"groupName":"Zanuvode &amp; More","artist":"Heka","groupYear":1988,
 "torrents":[{"torrentId":10,"media":"CD","format":"FLAC","encoding":"Lossless","hasLog":true,"logScore":100,"hasCue":true,"time":"2020-01-02 03:04:05","size":300,"seeders":7,"leechers":1},
 {"torrentId":11,"media":"WEB","format":"MP3","encoding":"320","remastered":true,"remasterYear":2020,"remasterTitle":"Remaster","time":"2020-01-02 03:04:05","size":100,"seeders":2,"leechers":0}]}]}}`

func TestSearch_AsksByArtistAndAlbumAndNamesEachEdition(t *testing.T) {
	server := &clienttest.Server{Routes: map[string]clienttest.Route{"/ajax.php": {Body: browse}}}
	red := gazelle.NewRedacted(httpClient(t, server), "https://redacted.sh", "key")
	got, err := red.Search(t.Context(), indexer.SearchRequest{Function: "music", Extra: map[string]string{"artist": "Heka", "album": "Zanuvode"}, Categories: []int{3000}})
	if err != nil || len(got) != 2 {
		t.Fatalf("Search = %+v, %v", got, err)
	}
	query, _ := url.ParseQuery(server.Seen[0].Query)
	if query.Get("artistname") != "Heka" || query.Get("groupname") != "Zanuvode" || server.Seen[0].Header.Get("Authorization") != "key" {
		t.Errorf("request = %+v", server.Seen[0])
	}
	if got[0].Title != "Heka - Zanuvode & More (1988) [CD FLAC Lossless] [Log 100%] [Cue]" || got[0].Categories[0] != 3040 ||
		got[0].Link != "https://redacted.sh/ajax.php?action=download&id=10" {
		t.Errorf("first = %+v", got[0])
	}
	if got[1].Title != "Heka - Zanuvode & More (2020) [WEB MP3 320] [Remaster]" || got[1].Categories[0] != 3010 {
		t.Errorf("second = %+v", got[1])
	}
}

// Orpheus takes its key as "token <key>", on the download too.
func TestOrpheus_SendsTheKeyAsAToken(t *testing.T) {
	server := &clienttest.Server{Routes: map[string]clienttest.Route{"/ajax.php": {Body: "d4:info"}}}
	ops := gazelle.NewOrpheus(httpClient(t, server), "https://orpheus.network", "key")
	if _, err := ops.Fetch(t.Context(), "https://orpheus.network/ajax.php?action=download&id=1"); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got := server.Seen[0].Header.Get("Authorization"); got != "token key" {
		t.Errorf("Authorization = %q", got)
	}
	server.Routes["/ajax.php"] = clienttest.Route{Body: `{"status":"failure","error":"bad credentials"}`}
	if _, err := ops.Capabilities(t.Context()); err == nil {
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
