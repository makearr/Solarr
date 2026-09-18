package sabnzbd_test

import (
	"context"
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/sabnzbd"
)

func TestVersion_ParsesAndSendsTheAPIKey(t *testing.T) {
	transport := &recordingTransport{body: `{"version": "4.3.2"}`}
	client := sabnzbd.NewClient(newTestClient(t, transport), "http://sabnzbd.local:8080", "test-api-key")

	version, err := client.Version(context.Background())
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if version != "4.3.2" {
		t.Errorf("Version = %q, want 4.3.2", version)
	}

	if len(transport.requested) != 1 {
		t.Fatalf("requested %d times, want 1", len(transport.requested))
	}
	query := transport.requested[0].URL.Query()
	if query.Get("mode") != "version" {
		t.Errorf("mode = %q, want version", query.Get("mode"))
	}
	if query.Get("output") != "json" {
		t.Errorf("output = %q, want json", query.Get("output"))
	}
	if query.Get("apikey") != "test-api-key" {
		t.Errorf("apikey = %q, want it sent", query.Get("apikey"))
	}
}
