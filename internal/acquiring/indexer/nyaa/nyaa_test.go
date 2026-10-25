package nyaa_test

import (
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/clienttest"
	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/indexer/nyaa"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

const feed = `<?xml version="1.0" encoding="UTF-8"?>
<rss xmlns:atom="http://www.w3.org/2005/Atom" xmlns:nyaa="https://nyaa.si/xmlns/nyaa" version="2.0"><channel>
<item><title>[Group] Show - 01 (1080p)</title><link>https://nyaa.si/download/1.torrent</link>
<guid isPermaLink="true">https://nyaa.si/view/1</guid><pubDate>Sat, 01 Jan 2022 00:00:00 -0000</pubDate>
<nyaa:seeders>40</nyaa:seeders><nyaa:leechers>2</nyaa:leechers><nyaa:infoHash>ABCDEF0123456789ABCDEF0123456789ABCDEF01</nyaa:infoHash>
<nyaa:categoryId>1_2</nyaa:categoryId><nyaa:size>1.5 GiB</nyaa:size></item>
<item><title>Album OST [FLAC]</title><link>https://nyaa.si/download/2.torrent</link><guid>https://nyaa.si/view/2</guid>
<pubDate>Sat, 01 Jan 2022 00:00:00 -0000</pubDate><nyaa:seeders>3</nyaa:seeders><nyaa:leechers>0</nyaa:leechers>
<nyaa:categoryId>2_1</nyaa:categoryId><nyaa:size>500 MiB</nyaa:size></item>
</channel></rss>`

func TestSearch_ReadsTheRSSAndKeepsTheRequestedCategory(t *testing.T) {
	server := &clienttest.Server{Routes: map[string]clienttest.Route{"/": {Body: feed}}}
	got, err := nyaa.New(httpClient(t, server), "https://nyaa.si").Search(t.Context(), indexer.SearchRequest{Query: "Show", Categories: []int{5070}})
	if err != nil || len(got) != 1 {
		t.Fatalf("Search = %+v, %v", got, err)
	}
	one := got[0]
	if one.Title != "[Group] Show - 01 (1080p)" || one.Link != "https://nyaa.si/download/1.torrent" || one.Seeders != 40 || one.Peers != 42 ||
		one.SizeBytes != 1610612736 || one.InfoHash != "abcdef0123456789abcdef0123456789abcdef01" || !one.PublishedAt.Equal(time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("item = %+v", one)
	}
	music, _ := nyaa.New(httpClient(t, server), "https://nyaa.si").Search(t.Context(), indexer.SearchRequest{Query: "Album", Categories: []int{3000}})
	if len(music) != 1 || music[0].Categories[0] != 3040 {
		t.Errorf("music = %+v", music)
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
