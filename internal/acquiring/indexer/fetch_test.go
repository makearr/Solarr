package indexer_test

import (
	"context"
	"io"
	"net/http"
	"strings"
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

type redirectingTransport struct{ location string }

func (f redirectingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	header := make(http.Header)
	header.Set("Location", f.location)
	return &http.Response{StatusCode: http.StatusFound, Header: header, Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
}

// Prowlarr answers a torrent's download with a redirect to its magnet link: that link is the release.
func TestFetch_AMagnetRedirectIsTheRelease(t *testing.T) {
	const magnet = "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=x"
	client := indexer.NewClient(newTestClient(t, redirectingTransport{location: magnet}), "http://indexer.local", "key")

	body, err := client.Fetch(context.Background(), "http://indexer.local/4/download?link=abc")
	if err != nil || string(body) != magnet {
		t.Fatalf("Fetch = %q, %v, want the magnet link", body, err)
	}
}
