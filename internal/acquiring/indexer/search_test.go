package indexer_test

import (
	"context"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
)

// searchFixtureXML is a representative t=search response -- RSS 2.0 with
// the Newznab attribute extension (ADR-43, shared by Torznab). The first
// item is a usenet-shaped result (no seeders/peers), the second a
// torrent-shaped one (Newznab's own size attr absent, using the enclosure
// length and seeders/peers/infohash instead) -- proof the same parse code
// covers both, since Torznab is the identical shape with different attrs
// populated.
const searchFixtureXML = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/">
  <channel>
    <title>Search results</title>
    <item>
      <title>Artist.Name-Album.Title-2020-FLAC-GROUP</title>
      <guid isPermaLink="true">https://indexer.example/details/abc123</guid>
      <link>https://indexer.example/details/abc123</link>
      <pubDate>Mon, 01 Jan 2024 12:00:00 +0000</pubDate>
      <enclosure url="https://indexer.example/getnzb/abc123.nzb&amp;i=1&amp;r=APIKEY" length="734003200" type="application/x-nzb"/>
      <newznab:attr name="category" value="3000"/>
      <newznab:attr name="category" value="3040"/>
      <newznab:attr name="size" value="734003200"/>
      <newznab:attr name="guid" value="abc123"/>
    </item>
    <item>
      <title>Other.Artist-Other.Album-2019-MP3-GROUP2</title>
      <guid isPermaLink="false">def456</guid>
      <link>magnet:?xt=urn:btih:def456</link>
      <pubDate>Tue, 02 Jan 2024 08:30:00 +0000</pubDate>
      <enclosure url="magnet:?xt=urn:btih:def456" length="52428800" type="application/x-bittorrent"/>
      <newznab:attr name="category" value="3010"/>
      <newznab:attr name="seeders" value="42"/>
      <newznab:attr name="peers" value="50"/>
      <newznab:attr name="infohash" value="def456"/>
    </item>
  </channel>
</rss>`

func TestSearch_ParsesUsenetAndTorrentShapedItems(t *testing.T) {
	transport := &recordingTransport{body: searchFixtureXML}
	client := indexer.NewClient(newTestClient(t, transport), "http://indexer.local", "test-api-key")

	items, err := client.Search(context.Background(), indexer.SearchRequest{
		Function: "music-search", Query: "Artist Name Album Title", Categories: []int{3000}, Limit: 50,
		Extra: map[string]string{"artist": "Artist Name", "album": "Album Title"},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(items))
	}

	query := transport.requested[0].URL.Query()
	if query.Get("t") != "music-search" {
		t.Errorf("t = %q, want music-search", query.Get("t"))
	}
	if query.Get("q") != "Artist Name Album Title" {
		t.Errorf("q = %q, want the query text", query.Get("q"))
	}
	if query.Get("cat") != "3000" {
		t.Errorf("cat = %q, want 3000", query.Get("cat"))
	}
	if query.Get("limit") != "50" {
		t.Errorf("limit = %q, want 50", query.Get("limit"))
	}
	if query.Get("artist") != "Artist Name" || query.Get("album") != "Album Title" {
		t.Errorf("artist/album = %q/%q, want them forwarded from Extra", query.Get("artist"), query.Get("album"))
	}

	usenet := items[0]
	if usenet.Title != "Artist.Name-Album.Title-2020-FLAC-GROUP" {
		t.Errorf("usenet.Title = %q", usenet.Title)
	}
	if usenet.GUID != "https://indexer.example/details/abc123" {
		t.Errorf("usenet.GUID = %q", usenet.GUID)
	}
	if usenet.SizeBytes != 734003200 {
		t.Errorf("usenet.SizeBytes = %d, want 734003200", usenet.SizeBytes)
	}
	if len(usenet.Categories) != 2 || usenet.Categories[0] != 3000 || usenet.Categories[1] != 3040 {
		t.Errorf("usenet.Categories = %v, want [3000 3040]", usenet.Categories)
	}
	if usenet.Seeders != 0 || usenet.Peers != 0 || usenet.InfoHash != "" {
		t.Errorf("usenet has torrent-only fields set: %+v", usenet)
	}
	wantPublished := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	if !usenet.PublishedAt.Equal(wantPublished) {
		t.Errorf("usenet.PublishedAt = %v, want %v", usenet.PublishedAt, wantPublished)
	}

	torrent := items[1]
	if torrent.SizeBytes != 52428800 {
		t.Errorf("torrent.SizeBytes = %d, want 52428800 (from the enclosure, no size attr present)", torrent.SizeBytes)
	}
	if torrent.Seeders != 42 || torrent.Peers != 50 || torrent.InfoHash != "def456" {
		t.Errorf("torrent = %+v, want Seeders=42 Peers=50 InfoHash=def456", torrent)
	}
}

func TestSearch_DefaultsToTheGeneralSearchFunction(t *testing.T) {
	transport := &recordingTransport{body: `<rss><channel></channel></rss>`}
	client := indexer.NewClient(newTestClient(t, transport), "http://indexer.local", "")

	if _, err := client.Search(context.Background(), indexer.SearchRequest{Query: "anything"}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got := transport.requested[0].URL.Query().Get("t"); got != "search" {
		t.Errorf("t = %q, want search (the default function)", got)
	}
}

// An item with a malformed or missing pubDate must not fail the whole
// search -- it is one fact among many, not a reason to lose every other
// result on the page.
func TestSearch_ToleratesAnUnparseablePubDate(t *testing.T) {
	const broken = `<?xml version="1.0"?>
<rss><channel><item>
  <title>Some.Release</title>
  <guid>xyz</guid>
  <pubDate>not a date</pubDate>
  <enclosure url="https://indexer.example/nzb" length="1000"/>
</item></channel></rss>`
	transport := &recordingTransport{body: broken}
	client := indexer.NewClient(newTestClient(t, transport), "http://indexer.local", "")

	items, err := client.Search(context.Background(), indexer.SearchRequest{Query: "x"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(items) != 1 || items[0].Title != "Some.Release" {
		t.Fatalf("items = %+v, want the one item still parsed", items)
	}
	if !items[0].PublishedAt.IsZero() {
		t.Errorf("PublishedAt = %v, want the zero time for an unparseable date", items[0].PublishedAt)
	}
}
