package sabnzbd_test

import (
	"context"
	"testing"

	"github.com/makearr/MediaNexus/internal/acquiring/sabnzbd"
)

// TestAddURL_SendsParamsAndReturnsTheClientRef exercises AddURL entirely
// against a fake transport -- this is ADR-57's own acquire.send, the one
// call in this package with a real side effect on a real downloader, and
// this test suite never points it at anything but the fake.
func TestAddURL_SendsParamsAndReturnsTheClientRef(t *testing.T) {
	transport := &recordingTransport{body: `{"status": true, "nzo_ids": ["SABnzbd_nzo_abc123"]}`}
	client := sabnzbd.NewClient(newTestClient(t, transport), "http://sabnzbd.local:8080", "test-api-key")

	clientRef, err := client.AddURL(context.Background(), sabnzbd.AddURLRequest{
		NZBURL: "https://indexer.example/getnzb/abc123.nzb", Category: "music", Priority: 1,
	})
	if err != nil {
		t.Fatalf("AddURL: %v", err)
	}
	if clientRef != "SABnzbd_nzo_abc123" {
		t.Errorf("clientRef = %q, want SABnzbd_nzo_abc123", clientRef)
	}

	query := transport.requested[0].URL.Query()
	if query.Get("mode") != "addurl" {
		t.Errorf("mode = %q, want addurl", query.Get("mode"))
	}
	if query.Get("name") != "https://indexer.example/getnzb/abc123.nzb" {
		t.Errorf("name = %q, want the NZB URL", query.Get("name"))
	}
	if query.Get("cat") != "music" {
		t.Errorf("cat = %q, want music", query.Get("cat"))
	}
	if query.Get("priority") != "1" {
		t.Errorf("priority = %q, want 1", query.Get("priority"))
	}
}

func TestAddURL_RefusalSurfacesSABnzbdsOwnError(t *testing.T) {
	transport := &recordingTransport{body: `{"status": false, "error": "NZB file not found"}`}
	client := sabnzbd.NewClient(newTestClient(t, transport), "http://sabnzbd.local:8080", "")

	_, err := client.AddURL(context.Background(), sabnzbd.AddURLRequest{NZBURL: "https://indexer.example/gone.nzb"})
	if err == nil {
		t.Fatal("AddURL succeeded, want the refusal surfaced")
	}
}
