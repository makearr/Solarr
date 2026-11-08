package indexer_test

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
)

// Trimmed from the real Prowlarr's /api/v1/indexer (2026-09-25): the fields read, as it sends them.
const prowlarrFixture = `[
 {"id":40,"name":"NZBgeek","protocol":"usenet","enable":true,"priority":5,
  "capabilities":{"categories":[{"id":2000,"name":"Movies"},{"id":3000,"name":"Audio"}]}},
 {"id":1,"name":"EZTV","protocol":"torrent","enable":false,"priority":99,
  "capabilities":{"categories":[{"id":5000,"name":"TV"}]}}
]`

func TestListProwlarr_EveryIndexerReachableThroughProwlarr(t *testing.T) {
	transport := &recordingTransport{body: prowlarrFixture}
	got, err := indexer.ListProwlarr(t.Context(), newTestClient(t, transport), "http://prowlarr.local:9696/", "secret")
	if err != nil {
		t.Fatalf("ListProwlarr: %v", err)
	}
	want := []indexer.ProwlarrIndexer{
		{ProwlarrID: 40, Name: "NZBgeek", Impl: "newznab", Enabled: true, Music: true, BaseURL: "http://prowlarr.local:9696/40", Priority: 5, Supported: true},
		{ProwlarrID: 1, Name: "EZTV", Impl: "torznab", Enabled: false, Music: false, BaseURL: "http://prowlarr.local:9696/1", Priority: 50, Supported: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
	request := transport.requested[0]
	if request.URL.String() != "http://prowlarr.local:9696/api/v1/indexer" || request.Header.Get("X-Api-Key") != "secret" {
		t.Errorf("asked %s with key %q", request.URL, request.Header.Get("X-Api-Key"))
	}
}

func TestSupportedCategory(t *testing.T) {
	for id, want := range map[int]bool{1000: false, 2040: true, 3010: true, 4050: false, 5030: true, 6000: true, 7020: true, 8010: true, 9000: false} {
		if got := indexer.SupportedCategory(id); got != want {
			t.Errorf("SupportedCategory(%d) = %v, want %v", id, got, want)
		}
	}
}
