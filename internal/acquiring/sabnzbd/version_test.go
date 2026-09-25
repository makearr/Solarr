package sabnzbd_test

import (
	"context"
	"errors"
	"slices"
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

// The responses the real SABnzbd gave 2026-09-25: its folder and categories, and a refused key.
func TestCompleteFolderAndCategories_AndARefusedKey(t *testing.T) {
	folder := sabnzbd.NewClient(newTestClient(t, &recordingTransport{body: `{"config":{"misc":{"complete_dir":"/downloads/sabnzbd"}}}`}),
		"http://sabnzbd.local:8080", "key")
	if got, err := folder.CompleteFolder(t.Context()); err != nil || got != "/downloads/sabnzbd" {
		t.Errorf("CompleteFolder = %q, %v; want /downloads/sabnzbd", got, err)
	}
	cats := sabnzbd.NewClient(newTestClient(t, &recordingTransport{body: `{"categories":["*","music","tv"]}`}),
		"http://sabnzbd.local:8080", "key")
	if got, err := cats.Categories(t.Context()); err != nil || !slices.Equal(got, []string{"*", "music", "tv"}) {
		t.Errorf("Categories = %v, %v", got, err)
	}
	refused := sabnzbd.NewClient(newTestClient(t, &recordingTransport{body: "API Key Incorrect\n"}), "http://sabnzbd.local:8080", "bad")
	if _, err := refused.CompleteFolder(t.Context()); !errors.Is(err, sabnzbd.ErrAPIKeyRefused) {
		t.Errorf("err = %v, want ErrAPIKeyRefused", err)
	}
}
