package sabnzbd_test

import (
	"context"
	"errors"
	"github.com/makearr/Solarr/internal/acquiring/download"
	"io"
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/sabnzbd"
)

// The NZB is uploaded as a file and the job named after the release, never after a URL.
func TestAddFile_UploadsTheNZBUnderTheReleaseName(t *testing.T) {
	transport := &recordingTransport{body: `{"status": true, "nzo_ids": ["SABnzbd_nzo_abc123"]}`}
	client := sabnzbd.NewClient(newTestClient(t, transport), "http://sabnzbd.local:8080", "test-api-key")

	clientRef, err := client.AddFile(context.Background(), sabnzbd.AddFileRequest{
		Name: "Heka-Zanuvode-1988-FLAC", NZB: []byte("<nzb/>"), Category: "music", Priority: 1,
	})
	if err != nil {
		t.Fatalf("AddFile: %v", err)
	}
	if clientRef != "SABnzbd_nzo_abc123" {
		t.Errorf("clientRef = %q, want SABnzbd_nzo_abc123", clientRef)
	}

	request := transport.requested[0]
	query := request.URL.Query()
	got := [5]string{request.Method, query.Get("mode"), query.Get("nzbname"), query.Get("cat"), query.Get("priority")}
	want := [5]string{"POST", "addfile", "Heka-Zanuvode-1988-FLAC", "music", "1"}
	if got != want {
		t.Errorf("request = %q, want %q", got, want)
	}
	file, header, err := request.FormFile("name")
	if err != nil {
		t.Fatalf("FormFile: %v", err)
	}
	content, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "<nzb/>" || header.Filename != "Heka-Zanuvode-1988-FLAC.nzb" {
		t.Errorf("uploaded %q as %q, want the NZB as Heka-Zanuvode-1988-FLAC.nzb", content, header.Filename)
	}
}

func TestAddFile_RefusalSurfacesSABnzbdsOwnError(t *testing.T) {
	transport := &recordingTransport{body: `{"status": false, "error": "NZB file not valid"}`}
	client := sabnzbd.NewClient(newTestClient(t, transport), "http://sabnzbd.local:8080", "")

	_, err := client.AddFile(context.Background(), sabnzbd.AddFileRequest{Name: "x", NZB: []byte("junk")})
	want := "sabnzbd: add file: the download client refused the release: NZB file not valid"
	if !errors.Is(err, download.ErrRefused) || err.Error() != want {
		t.Fatalf("err = %v, want %q as a refusal", err, want)
	}
}

// SABnzbd's duplicate detection answers status false with no reason at all (found live 2026-09-29).
func TestAddFile_ARefusalWithoutAReasonStillSaysWhatHappened(t *testing.T) {
	transport := &recordingTransport{body: `{"status": false}`}
	client := sabnzbd.NewClient(newTestClient(t, transport), "http://sabnzbd.local:8080", "")

	_, err := client.AddFile(context.Background(), sabnzbd.AddFileRequest{Name: "x", NZB: []byte("<nzb/>")})
	want := "sabnzbd: add file: the download client refused the release (SABnzbd gave no reason; often it has downloaded it before)"
	if !errors.Is(err, download.ErrRefused) || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
}
