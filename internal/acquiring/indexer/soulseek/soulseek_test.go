package soulseek_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/clienttest"
	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/indexer/soulseek"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

const responses = `[{"username":"peer","hasFreeUploadSlot":true,"queueLength":2,"files":[
 {"filename":"@@music\\Heka\\Zanuvode (1988)\\01 - Kelo.flac","size":30,"extension":"flac","bitDepth":16},
 {"filename":"@@music\\Heka\\Zanuvode (1988)\\02 - Mira.flac","size":40,"extension":"flac","bitDepth":16},
 {"filename":"@@music\\Heka\\Zanuvode (1988)\\cover.jpg","size":5,"extension":"jpg"},
 {"filename":"@@music\\Heka\\Live\\CD1\\01.mp3","size":9,"extension":"mp3","bitRate":320},
 {"filename":"@@music\\Heka\\Photos\\a.jpg","size":1,"extension":"jpg"},
 {"filename":"@@music\\Heka\\Locked\\01.flac","size":1,"extension":"flac","isLocked":true}]}]`

func TestSearch_IsOneReleasePerPeerFolderWithAudio(t *testing.T) {
	server := &clienttest.Server{}
	server.Answer = func(request clienttest.Request) (clienttest.Route, bool) {
		if strings.HasSuffix(request.Path, "/responses") {
			return clienttest.Route{Body: responses}, true
		}
		if request.Method == "POST" {
			return clienttest.Route{Body: `{"id":"x","isComplete":true}`}, true
		}
		return clienttest.Route{}, true
	}
	searcher := soulseek.New(httpClient(t, server), clock.NewFakeAtEpoch(), "http://slskd:5030", "key")
	got, err := searcher.Search(t.Context(), indexer.SearchRequest{Function: "music", Query: "Heka Zanuvode", Categories: []int{3000}})
	if err != nil || len(got) != 2 {
		t.Fatalf("Search = %+v, %v; want the album and the live disc", got, err)
	}
	if got[0].Title != "Heka - Zanuvode (1988) [FLAC]" || got[0].SizeBytes != 75 || got[0].Categories[0] != 3040 || got[0].Seeders != 1 {
		t.Errorf("album = %+v", got[0])
	}
	if got[1].Title != "Heka - Live [MP3 320]" || got[1].Categories[0] != 3010 {
		t.Errorf("live = %+v", got[1])
	}
	if !strings.Contains(server.Seen[0].Body, `"searchText":"Heka Zanuvode"`) || server.Seen[0].Header.Get("X-API-Key") != "key" {
		t.Errorf("start = %+v", server.Seen[0])
	}
	if last := server.Seen[len(server.Seen)-1]; last.Method != "DELETE" {
		t.Errorf("the search was not forgotten: last request %s %s", last.Method, last.Path)
	}

	file, err := searcher.Fetch(t.Context(), got[0].Link)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	release, err := download.ParseDirectRelease(file)
	if err != nil || release.Source != "peer" || len(release.Files) != 3 {
		t.Errorf("release = %+v, %v; want the folder's three files from the peer", release, err)
	}
	var raw map[string]any
	if json.Unmarshal(file, &raw) != nil {
		t.Error("the release is not JSON")
	}
}

func TestSearch_AnswersNothingForAFeedOrAnotherMediaType(t *testing.T) {
	server := &clienttest.Server{}
	searcher := soulseek.New(httpClient(t, server), clock.NewFakeAtEpoch(), "http://slskd:5030", "key")
	for _, request := range []indexer.SearchRequest{{}, {Query: "Show S01", Categories: []int{5000}}} {
		if got, err := searcher.Search(t.Context(), request); err != nil || got != nil {
			t.Errorf("%+v = %+v, %v", request, got, err)
		}
	}
	if len(server.Seen) != 0 {
		t.Errorf("slskd was asked %d times", len(server.Seen))
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
