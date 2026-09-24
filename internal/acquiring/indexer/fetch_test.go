package indexer_test

import (
	"context"
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
)

// The release file is fetched from exactly the link the search result gave, body returned whole.
func TestFetch_ReturnsTheLinkedFile(t *testing.T) {
	transport := &recordingTransport{body: "<nzb/>"}
	client := indexer.NewClient(newTestClient(t, transport), "http://indexer.local", "key")

	body, err := client.Fetch(context.Background(), "http://indexer.local/40/download?link=abc")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(body) != "<nzb/>" || transport.requested[0].URL.String() != "http://indexer.local/40/download?link=abc" {
		t.Errorf("fetched %q from %s, want <nzb/> from the given link", body, transport.requested[0].URL)
	}
}

// An empty answer is not an NZB; sending it on would only fail inside the download client.
func TestFetch_RefusesAnEmptyFile(t *testing.T) {
	client := indexer.NewClient(newTestClient(t, &recordingTransport{}), "http://indexer.local", "key")

	if _, err := client.Fetch(context.Background(), "http://indexer.local/x"); err == nil {
		t.Fatal("Fetch succeeded on an empty body")
	}
}
